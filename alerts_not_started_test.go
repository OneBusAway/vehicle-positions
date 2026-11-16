package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// notStartedSettings are main's defaults.
var notStartedSettings = alertSettings{
	quietAfter:       defaultAlertQuietAfter,
	stoppedAfter:     defaultAlertStoppedAfter,
	offRouteDistance: rider.DefaultThresholds().MaxShapeDistance,
	offRouteAfter:    defaultAlertOffRouteAfter,
	notStartedAfter:  defaultAlertNotStartedAfter,
}

// feedZone is the test feed's timezone.
var feedZone, _ = time.LoadLocation("America/Los_Angeles")

// onBlockDay is a time on Wednesday 18 November 2026, a weekday, in the test
// feed's timezone.
func onBlockDay(h, m, s int) time.Time {
	return time.Date(2026, 11, 18, h, m, s, 0, feedZone)
}

// blockEdit chains the test feed's North trips into block B1 on weekdays: T1
// at 08:00, T7 at 08:30 and T8 at 09:10, each ten minutes long, with T2, which
// runs at 09:00 on Saturdays only, in the same block. T3 is on block B2, and
// T4 at 10:00 and T6 at 10:30 on none.
func blockEdit(name string, body []byte) []byte {
	switch name {
	case "trips.txt":
		blocks := map[string]string{"T1": "B1", "T2": "B1", "T3": "B2"}
		lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
		lines[0] += ",block_id"
		for i := 1; i < len(lines); i++ {
			lines[i] += "," + blocks[strings.Split(lines[i], ",")[2]]
		}
		lines = append(lines, "R1,WEEKDAY,T7,S1,North,0,B1", "R1,WEEKDAY,T8,S1,North,0,B1", "R1,WEEKDAY,T6,S1,North,0,")
		return []byte(strings.Join(lines, "\n") + "\n")
	case "stop_times.txt":
		return []byte(strings.TrimRight(string(body), "\n") + "\n" +
			"T7,08:30:00,08:30:00,ST1,1,0.000000\nT7,08:40:00,08:40:00,ST3,2,1001.000000\n" +
			"T8,09:10:00,09:10:00,ST1,1,0.000000\nT8,09:20:00,09:20:00,ST3,2,1001.000000\n" +
			"T6,10:30:00,10:30:00,ST1,1,0.000000\nT6,10:40:00,10:40:00,ST3,2,1001.000000\n")
	}
	return body
}

func blockFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	return fixtureIndexEdited(t, blockEdit)
}

// newBlockWatcher is a watcher with main's defaults, reading the schedule ix.
func newBlockWatcher(ix *rider.Index) *alertWatcher {
	w := newAlertWatcher(&fakeTripLister{}, notStartedSettings)
	w.watchRoutes(func() *rider.Index { return ix })
	return w
}

// blockRun is trip id, of GTFS trip gtfsTrip on vehicleID, started at start
// and ended at ended, or still running when ended is zero.
func blockRun(id int64, vehicleID, gtfsTrip string, start, ended time.Time) TripSummary {
	trip := TripSummary{
		ID:           id,
		VehicleID:    vehicleID,
		VehicleLabel: "Van " + strings.TrimPrefix(vehicleID, "van-"),
		UserID:       3,
		DriverName:   "Achieng",
		RouteID:      "R1",
		GtfsTripID:   gtfsTrip,
		StartTime:    start,
		Status:       "active",
	}
	if !ended.IsZero() {
		trip.EndTime = &ended
		trip.Status = "completed"
	}
	return trip
}

// t1Run is van-7's run of T1, a minute late and done by 08:12.
func t1Run() TripSummary {
	return blockRun(7, "van-7", "T1", onBlockDay(8, 1, 0), onBlockDay(8, 12, 0))
}

func TestAlerts_NotStarted(t *testing.T) {
	w := newBlockWatcher(blockFixtureIndex(t))
	run := t1Run()
	due := onBlockDay(8, 30, 0) // T7
	after := notStartedSettings.notStartedAfter

	tests := []struct {
		name     string
		sinceDue time.Duration
		want     bool
	}{
		{"just under", after - time.Second, false},
		{"exactly at", after, false},
		{"just over", after + time.Second, true},
		{"a second before T7 is due to arrive", 10*time.Minute - time.Second, true},
		{"when T7 is due to arrive", 10 * time.Minute, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := due.Add(tc.sinceDue)
			got := w.notStarted(nil, []TripSummary{run}, now)

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertNotStarted, got[0].Kind)
			assert.Equal(t, due.UTC(), got[0].Since)
			assert.Equal(t, int64(tc.sinceDue/time.Second), got[0].DurationSeconds)
			assert.Equal(t, run, got[0].Trip, "the trip the vehicle ran last")
			assert.Equal(t, &alertNextTrip{GtfsTripID: "T7", RouteID: "R1", BlockID: "B1"}, got[0].NextTrip)
		})
	}
}

// TestAlerts_NotStartedWhileTheVehicleRuns: a vehicle with a trip running is
// busy, on its block or off it.
func TestAlerts_NotStartedWhileTheVehicleRuns(t *testing.T) {
	w := newBlockWatcher(blockFixtureIndex(t))
	late := onBlockDay(8, 36, 0)

	t.Run("its last trip still running", func(t *testing.T) {
		running := blockRun(7, "van-7", "T1", onBlockDay(8, 1, 0), time.Time{})
		assert.Empty(t, w.notStarted([]TripSummary{running}, []TripSummary{running}, late))
	})

	t.Run("a trip off the schedule running", func(t *testing.T) {
		other := blockRun(8, "van-7", "", onBlockDay(8, 20, 0), time.Time{})
		assert.Empty(t, w.notStarted([]TripSummary{other}, []TripSummary{t1Run()}, late))
	})
}

// TestAlerts_NotStartedMovesOnWithTheBlock: once the next trip has started,
// whichever vehicle started it, the block's following trip is the one to
// watch, and the vehicle that ran the block's latest trip is the one working
// it. Trips that do not run that day are passed over.
func TestAlerts_NotStartedMovesOnWithTheBlock(t *testing.T) {
	w := newBlockWatcher(blockFixtureIndex(t))
	relief := blockRun(8, "van-8", "T7", onBlockDay(8, 30, 0), onBlockDay(8, 41, 0))
	recent := []TripSummary{t1Run(), relief}

	assert.Empty(t, w.notStarted(nil, recent, onBlockDay(8, 36, 0)), "T7 left on time")

	due := onBlockDay(9, 10, 0) // T8: T2 at 09:00 runs on Saturdays only
	got := w.notStarted(nil, recent, due.Add(notStartedSettings.notStartedAfter+time.Second))
	require.Len(t, got, 1, "one alert for the block, not one per vehicle that ran it")
	assert.Equal(t, relief, got[0].Trip)
	assert.Equal(t, "T8", got[0].NextTrip.GtfsTripID)
	assert.Equal(t, due.UTC(), got[0].Since)
}

// TestAlerts_NotStartedAfterARerun: a trip run twice by one vehicle, after an
// end by mistake or a relief mid-trip, gives the block to the later run,
// whatever order the trips are listed in.
func TestAlerts_NotStartedAfterARerun(t *testing.T) {
	w := newBlockWatcher(blockFixtureIndex(t))
	first := blockRun(7, "van-7", "T1", onBlockDay(8, 1, 0), onBlockDay(8, 3, 0))
	again := blockRun(8, "van-7", "T1", onBlockDay(8, 4, 0), onBlockDay(8, 13, 0))

	tests := []struct {
		name   string
		recent []TripSummary
	}{
		{"listed by start", []TripSummary{first, again}},
		{"listed the other way", []TripSummary{again, first}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := w.notStarted(nil, tc.recent, onBlockDay(8, 36, 0))
			require.Len(t, got, 1, "T7 is still due")
			assert.Equal(t, again, got[0].Trip)
			assert.Equal(t, "T7", got[0].NextTrip.GtfsTripID)
		})
	}
}

// TestAlerts_NotStartedRerunByAnotherVehicle: a second vehicle that reruns a
// trip, after a breakdown say, takes its block over, whether the first vehicle
// stands idle or has gone on to something else.
func TestAlerts_NotStartedRerunByAnotherVehicle(t *testing.T) {
	w := newBlockWatcher(blockFixtureIndex(t))
	brokeDown := blockRun(7, "van-7", "T1", onBlockDay(8, 1, 0), onBlockDay(8, 5, 0))
	rescue := blockRun(8, "van-8", "T1", onBlockDay(8, 10, 0), onBlockDay(8, 20, 0))

	tests := []struct {
		name   string
		active []TripSummary
	}{
		{"first vehicle idle", nil},
		{"first vehicle busy elsewhere", []TripSummary{blockRun(9, "van-7", "", onBlockDay(8, 30, 0), time.Time{})}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := w.notStarted(tc.active, []TripSummary{brokeDown, rescue}, onBlockDay(8, 36, 0))
			require.Len(t, got, 1)
			assert.Equal(t, rescue, got[0].Trip)
		})
	}
}

// TestAlerts_NotStartedFollowsTheVehicle: a vehicle whose latest trip is on
// another block, or on none, is no longer working the block.
func TestAlerts_NotStartedFollowsTheVehicle(t *testing.T) {
	w := newBlockWatcher(blockFixtureIndex(t))
	later := blockRun(8, "van-7", "T4", onBlockDay(8, 15, 0), onBlockDay(8, 25, 0))

	assert.Empty(t, w.notStarted(nil, []TripSummary{t1Run(), later}, onBlockDay(8, 36, 0)))
}

// TestAlerts_NotStartedNeedsABlock: only a trip on a block the schedule gives,
// with a next trip that day, has a next trip to be late with.
func TestAlerts_NotStartedNeedsABlock(t *testing.T) {
	blocks := blockFixtureIndex(t)
	headway := fixtureIndexEdited(t, blockEdit, fixtureMember{"frequencies.txt",
		"trip_id,start_time,end_time,headway_secs,exact_times\nT1,06:00:00,22:00:00,600,0\n"})
	require.True(t, mustTrip(t, headway, "T1").FrequencyBased)
	require.Equal(t, "B1", mustTrip(t, headway, "T1").BlockID)
	headwayNext := fixtureIndexEdited(t, blockEdit, fixtureMember{"frequencies.txt",
		"trip_id,start_time,end_time,headway_secs,exact_times\nT7,08:30:00,09:00:00,600,0\n"})
	require.True(t, mustTrip(t, headwayNext, "T7").FrequencyBased)

	tests := []struct {
		name string
		ix   *rider.Index
		run  TripSummary
		now  time.Time
	}{
		{"trip on no block, with another after it", blocks, blockRun(7, "van-7", "T4", onBlockDay(10, 0, 0), onBlockDay(10, 11, 0)), onBlockDay(10, 36, 0)},
		{"last trip of its block", blocks, blockRun(7, "van-7", "T8", onBlockDay(9, 10, 0), onBlockDay(9, 21, 0)), onBlockDay(9, 40, 0)},
		{"headway trip", headway, t1Run(), onBlockDay(8, 36, 0)},
		{"headway trip next", headwayNext, t1Run(), onBlockDay(8, 36, 0)},
		{"trip not in the schedule", blocks, blockRun(7, "van-7", "T9", onBlockDay(8, 1, 0), onBlockDay(8, 12, 0)), onBlockDay(8, 36, 0)},
		{"no schedule loaded", nil, t1Run(), onBlockDay(8, 36, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newBlockWatcher(tc.ix)
			assert.Empty(t, w.notStarted(nil, []TripSummary{tc.run}, tc.now))
		})
	}

	t.Run("watcher never given a schedule", func(t *testing.T) {
		w := newAlertWatcher(&fakeTripLister{}, notStartedSettings)
		assert.Empty(t, w.notStarted(nil, []TripSummary{t1Run()}, onBlockDay(8, 36, 0)))
	})
}

// blockTripsStore lists active trips as fakeTripLister does, and the trips
// started in a window from recent, noting the window it was asked for.
type blockTripsStore struct {
	fakeTripLister
	recent   []TripSummary
	err      error
	from, to time.Time
}

func (s *blockTripsStore) ListTripsStartedBetween(_ context.Context, from, to time.Time) ([]TripSummary, error) {
	s.from, s.to = from, to
	if s.err != nil {
		return nil, s.err
	}
	return s.recent, nil
}

func TestAlerts_ListIncludesNotStarted(t *testing.T) {
	ix := blockFixtureIndex(t)
	relief := blockRun(8, "van-8", "T7", onBlockDay(8, 30, 0), onBlockDay(8, 41, 0))
	now := onBlockDay(9, 16, 0) // T8 was due at 09:10

	t.Run("with blocks watched", func(t *testing.T) {
		// van-9 has been on a trip since 09:12 without a report, so it went
		// quiet after T8 was due.
		quiet := blockRun(9, "van-9", "", onBlockDay(9, 12, 0), time.Time{})
		store := &blockTripsStore{recent: []TripSummary{t1Run(), relief, quiet}}
		store.trips = []TripSummary{quiet}
		w := newAlertWatcher(store, notStartedSettings)
		w.watchingSince = onBlockDay(7, 0, 0)
		w.watchRoutes(func() *rider.Index { return ix })
		w.watchBlocks(store)

		got, err := w.list(context.Background(), now)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, alertNotStarted, got[0].Kind, "longest-running first, whatever the kind")
		assert.Equal(t, alertQuiet, got[1].Kind)
		assert.Equal(t, now.Add(-notStartedLookback), store.from)
		assert.Equal(t, now, store.to)
	})

	t.Run("without", func(t *testing.T) {
		store := &blockTripsStore{recent: []TripSummary{t1Run(), relief}}
		w := newAlertWatcher(store, notStartedSettings)
		w.watchRoutes(func() *rider.Index { return ix })

		got, err := w.list(context.Background(), now)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("the trips cannot be read", func(t *testing.T) {
		store := &blockTripsStore{err: errors.New("database down")}
		w := newAlertWatcher(store, notStartedSettings)
		w.watchRoutes(func() *rider.Index { return ix })
		w.watchBlocks(store)

		_, err := w.list(context.Background(), now)
		assert.ErrorContains(t, err, "database down")
	})
}

func TestAlertSettingsFromEnv_NotStartedAfter(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", defaultAlertNotStartedAfter},
		{"3m", 3 * time.Minute},
		{"90s", 90 * time.Second},
		{"0s", defaultAlertNotStartedAfter},
		{"-1m", defaultAlertNotStartedAfter},
		{"late", defaultAlertNotStartedAfter},
	}
	for _, tc := range tests {
		t.Run("ALERT_NOT_STARTED_AFTER="+tc.value, func(t *testing.T) {
			t.Setenv("ALERT_NOT_STARTED_AFTER", tc.value)
			assert.Equal(t, tc.want, alertSettingsFromEnv(60).notStartedAfter)
		})
	}
}

// TestAlerts_NotStartedFromStore runs the alert over a real trip: the trips the
// store lists as started must carry what the alert needs.
func TestAlerts_NotStartedFromStore(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "R1", "T1")
	require.NoError(t, err)
	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)
	// The trip ran T1 a minute late on the test day.
	_, err = store.pool.Exec(ctx, `UPDATE trips SET start_time = $1, end_time = $2 WHERE id = $3`,
		onBlockDay(8, 1, 0), onBlockDay(8, 12, 0), trip.ID)
	require.NoError(t, err)

	w := newAlertWatcher(store, notStartedSettings)
	ix := blockFixtureIndex(t)
	w.watchRoutes(func() *rider.Index { return ix })
	w.watchBlocks(store)

	got, err := w.list(ctx, onBlockDay(8, 36, 0))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, alertNotStarted, got[0].Kind)
	assert.Equal(t, trip.ID, got[0].Trip.ID)
	assert.Equal(t, "bus-trip-1", got[0].Trip.VehicleID)
	assert.Equal(t, "Bus 1", got[0].Trip.VehicleLabel)
	assert.Equal(t, "Trip Driver", got[0].Trip.DriverName)
	assert.Equal(t, "T7", got[0].NextTrip.GtfsTripID)
}

// TestAlerts_NotStartedConcurrent lists alerts while reports come in, for the
// race detector.
func TestAlerts_NotStartedConcurrent(t *testing.T) {
	ix := blockFixtureIndex(t)
	store := &blockTripsStore{recent: []TripSummary{t1Run()}}
	w := newAlertWatcher(store, notStartedSettings)
	w.watchRoutes(func() *rider.Index { return ix })
	w.watchBlocks(store)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for n := range 200 {
			pos := alongT1(float64(n), 5)
			w.report(&LocationReport{VehicleID: "van-7", TripID: "T7", Latitude: pos.Lat, Longitude: pos.Lon}, onBlockDay(8, 30, n))
		}
	}()
	go func() {
		defer wg.Done()
		for n := range 200 {
			_, err := w.list(context.Background(), onBlockDay(8, 36, n))
			assert.NoError(t, err)
		}
	}()
	wg.Wait()
}

// TestOpenAPI_AlertNextTrip holds the next_trip schema to the struct it
// documents.
func TestOpenAPI_AlertNextTrip(t *testing.T) {
	spec := loadOpenAPISpec(t)
	schema, ok := spec.mappings()["#/components/schemas/AlertNextTrip"]
	require.True(t, ok, "schema AlertNextTrip must exist")
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)

	documented := make([]string, 0, len(properties))
	for name := range properties {
		documented = append(documented, name)
	}
	marshalled := make([]string, 0)
	for _, field := range structJSONFields(t)["alertNextTrip"] {
		marshalled = append(marshalled, field.name)
	}
	assert.ElementsMatch(t, marshalled, documented)
	assert.ElementsMatch(t, []any{"gtfs_trip_id", "route_id", "block_id"}, schema["required"])
}
