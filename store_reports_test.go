package main

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// insertReportTrip stores a trip with the start and end given, as a driver
// starting and ending it then would have.
func insertReportTrip(t *testing.T, store *Store, userID int64, vehicleID, routeID, gtfsTripID string, start time.Time, end *time.Time) int64 {
	t.Helper()
	status := "completed"
	if end == nil {
		status = "active"
	}
	var id int64
	err := store.pool.QueryRow(context.Background(),
		`INSERT INTO trips (user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		userID, vehicleID, routeID, gtfsTripID, start, end, status).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestStore_ListTripsStartedBetween(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	driver := setupTripUser(t, store, "Report Driver", "report-driver@test.com", "bus-report-1", "Bus Report 1")
	from := time.Date(2026, 9, 2, 7, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	ended := from.Add(2 * time.Hour)

	insertReportTrip(t, store, driver, "bus-report-1", "R1", "BEFORE", from.Add(-time.Second), &ended)
	first := insertReportTrip(t, store, driver, "bus-report-1", "R1", "AT_FROM", from, &ended)
	tie := insertReportTrip(t, store, driver, "bus-report-1", "R1", "AT_FROM_TOO", from, &ended)
	insertReportTrip(t, store, driver, "bus-report-1", "R1", "", from.Add(time.Hour), &ended)
	last := insertReportTrip(t, store, driver, "bus-report-1", "R1", "BEFORE_TO", to.Add(-time.Second), nil)
	insertReportTrip(t, store, driver, "bus-report-1", "R1", "AT_TO", to, &ended)

	trips, err := store.ListTripsStartedBetween(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, trips, 3, "from is in the window, to is not, and a trip with no GTFS trip is left out")
	assert.Equal(t, []int64{first, tie, last}, []int64{trips[0].ID, trips[1].ID, trips[2].ID}, "oldest first, ties by id")

	assert.Equal(t, "AT_FROM", trips[0].GtfsTripID)
	assert.Equal(t, "bus-report-1", trips[0].VehicleID)
	assert.Equal(t, "Bus Report 1", trips[0].VehicleLabel)
	assert.Equal(t, driver, trips[0].UserID)
	assert.Equal(t, "Report Driver", trips[0].DriverName)
	assert.Equal(t, "R1", trips[0].RouteID)
	assert.True(t, from.Equal(trips[0].StartTime))
	require.NotNil(t, trips[0].EndTime)
	assert.True(t, ended.Equal(*trips[0].EndTime))
	assert.Equal(t, "completed", trips[0].Status)
	assert.Nil(t, trips[2].EndTime, "a trip still running has no end")
	assert.Equal(t, "active", trips[2].Status)

	none, err := store.ListTripsStartedBetween(ctx, to.Add(time.Hour), to.Add(2*time.Hour))
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

// TestServiceReporter_ReadsTheFixtureDayFromTheStore stores the fixture day,
// each run's points received a second after the phone took them, and checks
// the report worked out from the database is the one worked out from memory:
// the trips are read around the day and each trail is the one the Trips
// page's View trail draws.
func TestServiceReporter_ReadsTheFixtureDayFromTheStore(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	ix := reportFixtureIndex(t)
	trips, trails := reportFixtureTrips(t, ix)
	storeReportDay(t, store, trips, trails)

	memory := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	want, err := memory.report(ctx, "2026-09-02")
	require.NoError(t, err)
	fromStore := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	got, err := fromStore.report(ctx, "2026-09-02")
	require.NoError(t, err)

	require.NotZero(t, want.Routes[1].TripsRun)
	assertSameReport(t, want, got)

	// The day is long settled, so its totals were kept, and read back the
	// same through service_day_totals.
	fromStore.saving.Wait()
	kept, err := store.ListServiceDays(ctx, reportDay, reportDay)
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, newDayTotals(want, testReportSettings), kept[0].Totals)
}

// TestServiceReporter_ReadsTheHeadwayGapsFromTheStore: route 5's gaps are
// the same worked out from the database, where a trip's end time and its
// trail come from their own tables, as from memory, down to each run its
// day lists, and so are the gap totals kept through service_day_totals.
func TestServiceReporter_ReadsTheHeadwayGapsFromTheStore(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	ix := gapFixtureIndex(t)
	trips, trails := gapFixtureTrips(t, ix)
	storeReportDay(t, store, trips, trails)

	memory := newServiceReporter(&fakeReportStore{trips: trips, trails: trails}, func() *rider.Index { return ix }, testReportSettings)
	want, err := memory.report(ctx, "2026-09-02")
	require.NoError(t, err)
	fromStore := newServiceReporter(store, func() *rider.Index { return ix }, testReportSettings)
	got, err := fromStore.report(ctx, "2026-09-02")
	require.NoError(t, err)

	assert.Contains(t, routeGapsJSON(t, reportRoute(t, got, "R5")), `"gaps_judged":13`)
	assertSameReport(t, want, got)

	fromStore.saving.Wait()
	kept, err := store.ListServiceDays(ctx, reportDay, reportDay)
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, newDayTotals(want, testReportSettings), kept[0].Totals)
	encoded, err := json.Marshal(kept[0].Totals)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"gap_ratios":[{"tenth":2,"gaps":5}`)
}

// storeReportDay stores trips with their points, each received a second
// after the phone took it. A trip that has no end time ends a minute after
// its last point, or an hour after it started when it has none.
func storeReportDay(t *testing.T, store *Store, trips []TripSummary, trails map[int64][]LocationPoint) {
	t.Helper()
	ctx := context.Background()
	users := make(map[string]int64)
	for _, tr := range trips {
		if _, ok := users[tr.VehicleID]; !ok {
			users[tr.VehicleID] = setupTripUser(t, store, tr.DriverName, tr.VehicleID+"@test.com", tr.VehicleID, tr.VehicleLabel)
		}
		points := trails[tr.ID]
		end := tr.StartTime.Add(time.Hour)
		switch {
		case tr.EndTime != nil:
			end = *tr.EndTime
		case len(points) > 0:
			end = time.Unix(points[len(points)-1].Timestamp, 0).Add(time.Minute)
		}
		id := insertReportTrip(t, store, users[tr.VehicleID], tr.VehicleID, tr.RouteID, tr.GtfsTripID, tr.StartTime, &end)

		batch := &pgx.Batch{}
		for _, p := range points {
			batch.Queue(`INSERT INTO location_points (vehicle_id, driver_id, trip_id, latitude, longitude, timestamp, received_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				tr.VehicleID, strconv.FormatInt(users[tr.VehicleID], 10), tr.GtfsTripID, p.Latitude, p.Longitude, p.Timestamp,
				time.Unix(p.Timestamp, 0).Add(time.Second))
		}
		require.NoError(t, store.pool.SendBatch(ctx, batch).Close())

		stored, err := store.ListTripLocations(ctx, id)
		require.NoError(t, err)
		require.Len(t, stored, len(points), "trip %s's trail", tr.GtfsTripID)
	}
}

// assertSameReport holds a report worked out from the database to the one
// worked out from memory, route by route.
func assertSameReport(t *testing.T, want, got *serviceReport) {
	t.Helper()
	require.Len(t, got.Routes, len(want.Routes))
	for i := range want.Routes {
		wantRoute, gotRoute := want.Routes[i], got.Routes[i]
		require.Len(t, gotRoute.Trips, len(wantRoute.Trips))
		// The database numbers its trips its own way; the vehicle says which
		// run is which.
		ids := make(map[int64]int64)
		for j := range wantRoute.Trips {
			require.Len(t, gotRoute.Trips[j].Runs, len(wantRoute.Trips[j].Runs))
			for k := range wantRoute.Trips[j].Runs {
				assert.Equal(t, wantRoute.Trips[j].Runs[k].VehicleID, gotRoute.Trips[j].Runs[k].VehicleID)
				ids[gotRoute.Trips[j].Runs[k].ID] = wantRoute.Trips[j].Runs[k].ID
				gotRoute.Trips[j].Runs[k].ID = wantRoute.Trips[j].Runs[k].ID
			}
		}
		for _, d := range gotRoute.Gaps {
			for r := range d.runs {
				run := &d.runs[r]
				run.id = ids[run.id]
				if run.gap != nil {
					run.gap.run, run.gap.ahead = ids[run.gap.run], ids[run.gap.ahead]
				}
			}
		}
		assert.Equal(t, wantRoute, gotRoute, "route %s", wantRoute.RouteID)
	}
}
