package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// offRouteSettings are main's defaults, with the off-route distance that
// RIDER_MAX_SHAPE_DISTANCE defaults to.
var offRouteSettings = alertSettings{
	quietAfter:       defaultAlertQuietAfter,
	stoppedAfter:     defaultAlertStoppedAfter,
	offRouteDistance: rider.DefaultThresholds().MaxShapeDistance,
	offRouteAfter:    defaultAlertOffRouteAfter,
}

// t1Start is where T1's shape in the test feed starts. The shape runs due
// north from there for 1001 m.
var t1Start = rider.LatLon{Lat: 47.6, Lon: -122.33}

// alongT1 is a fix north metres up T1's shape and east metres east of it.
// While north is within the shape's length, east is its distance from the
// shape, give or take the metre that OffsetMetres and the projection disagree
// by, so a distance used at a threshold stays a few metres clear of it.
func alongT1(north, east float64) rider.LatLon {
	return rider.OffsetMetres(t1Start, north, east)
}

// newRouteWatcher is newTestWatcher with offRouteSettings, judging reports
// against the trips in ix.
func newRouteWatcher(ix *rider.Index) *alertWatcher {
	w := newTestWatcher()
	w.settings = offRouteSettings
	w.watchRoutes(func() *rider.Index { return ix })
	return w
}

// newJoinedWatcher is newRouteWatcher with bus-7 already near T1's shape, 30 s
// into a trip that started at alertT0: a vehicle can only go off a route it
// has been on.
func newJoinedWatcher(ix *rider.Index) *alertWatcher {
	w := newRouteWatcher(ix)
	w.report(routeFix("T1", alongT1(10, 3), nil), alertT0.Add(30*time.Second))
	return w
}

func plainFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	return fixtureIndexEdited(t, func(_ string, body []byte) []byte { return body })
}

// routeFix is a report from bus-7 at pos on GTFS trip tripID, on a run of it
// that started at alertT0.
func routeFix(tripID string, pos rider.LatLon, accuracy *float64) *LocationReport {
	return &LocationReport{VehicleID: "bus-7", TripID: tripID, RouteID: "R1", Latitude: pos.Lat, Longitude: pos.Lon, Accuracy: accuracy, TripStartedAt: alertT0}
}

// onRun is fix on the run of its trip that started at start.
func onRun(fix *LocationReport, start time.Time) *LocationReport {
	fix.TripStartedAt = start
	return fix
}

// driveT1 has bus-7 report on T1 every 5 s from from to to, both included,
// east metres east of the shape. It starts north metres up the shape and goes
// 2 m further with each report, so it is never standing still, and it returns
// how far up the shape it got.
func driveT1(w *alertWatcher, from, to time.Time, north, east float64, accuracy *float64) float64 {
	for at := from; !at.After(to); at = at.Add(5 * time.Second) {
		w.report(routeFix("T1", alongT1(north, east), accuracy), at)
		north += 2
	}
	return north
}

func metres(m float64) *float64 { return &m }

func TestAlerts_OffRoute(t *testing.T) {
	ix := plainFixtureIndex(t)
	after := offRouteSettings.offRouteAfter
	firstOff := alertT0.Add(time.Minute)

	tests := []struct {
		name string
		// lastOff is when the second report, also off route, comes after the
		// first.
		lastOff time.Duration
		want    bool
	}{
		{"just under", after - time.Second, false},
		{"exactly at", after, false},
		{"just over", after + time.Second, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newJoinedWatcher(ix)
			trip := alertTrip("bus-7", alertT0)
			lastOff := firstOff.Add(tc.lastOff)
			w.report(routeFix("T1", alongT1(100, 200), nil), firstOff)
			w.report(routeFix("T1", alongT1(400, 200), nil), lastOff)

			got := w.alerts([]TripSummary{trip}, lastOff)

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertOffRoute, got[0].Kind)
			assert.Equal(t, firstOff, got[0].Since)
			assert.Equal(t, int64(tc.lastOff/time.Second), got[0].DurationSeconds)
			assert.Equal(t, trip, got[0].Trip)
		})
	}
}

// TestAlerts_OffRouteCountsOnlyReportedTime: the vehicle may have come back
// to its route in the silence after its latest report, so the time off route
// runs to that report and not to now.
func TestAlerts_OffRouteCountsOnlyReportedTime(t *testing.T) {
	w := newJoinedWatcher(plainFixtureIndex(t))
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	firstOff := alertT0.Add(time.Minute)
	lastOff := firstOff.Add(4 * time.Minute)
	w.report(routeFix("T1", alongT1(100, 200), nil), firstOff)
	w.report(routeFix("T1", alongT1(400, 200), nil), lastOff)

	silentUntil := lastOff.Add(offRouteSettings.quietAfter - time.Second)
	require.Greater(t, silentUntil.Sub(firstOff), offRouteSettings.offRouteAfter, "now is past the threshold, the reports are not")
	assert.Empty(t, w.alerts(trips, silentUntil))
}

// TestAlerts_OffRouteStays: once raised, the alert keeps its start while the
// vehicle stays off route, through fixes that cannot tell either way.
func TestAlerts_OffRouteStays(t *testing.T) {
	w := newJoinedWatcher(plainFixtureIndex(t))
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	firstOff := alertT0.Add(time.Minute)
	raised := firstOff.Add(offRouteSettings.offRouteAfter + 5*time.Second)
	north := driveT1(w, firstOff, raised, 50, 200, nil)
	require.Len(t, w.alerts(trips, raised), 1)

	tests := []struct {
		name     string
		east     float64
		accuracy *float64
	}{
		{"still off route", 200, nil},
		{"a poor fix on the route", 5, metres(100)},
		{"a fix just past the distance, within its accuracy", 75, metres(20)},
		{"off route again", 300, nil},
	}
	at := raised
	for _, tc := range tests {
		at = at.Add(time.Minute)
		north = driveT1(w, at.Add(-55*time.Second), at, north, tc.east, tc.accuracy)

		got := w.alerts(trips, at)
		require.Len(t, got, 1, tc.name)
		assert.Equal(t, alertOffRoute, got[0].Kind, tc.name)
		assert.Equal(t, firstOff, got[0].Since, tc.name)
		assert.Equal(t, int64(at.Sub(firstOff)/time.Second), got[0].DurationSeconds, tc.name)
	}
}

func TestAlerts_OffRouteClearsOnReturn(t *testing.T) {
	w := newJoinedWatcher(plainFixtureIndex(t))
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	firstOff := alertT0.Add(time.Minute)
	raised := firstOff.Add(offRouteSettings.offRouteAfter + 5*time.Second)
	north := driveT1(w, firstOff, raised, 50, 200, nil)
	require.Len(t, w.alerts(trips, raised), 1)

	back := raised.Add(5 * time.Second)
	w.report(routeFix("T1", alongT1(north, 5), metres(10)), back)
	assert.Empty(t, w.alerts(trips, back), "one good fix near the shape clears it")

	// Leaving the route again starts a new spell, from its first report.
	offAgain := back.Add(5 * time.Second)
	driveT1(w, offAgain, offAgain.Add(offRouteSettings.offRouteAfter), north+2, 200, nil)
	assert.Empty(t, w.alerts(trips, offAgain.Add(offRouteSettings.offRouteAfter)))
	w.report(routeFix("T1", alongT1(north+300, 200), nil), offAgain.Add(offRouteSettings.offRouteAfter+time.Second))
	got := w.alerts(trips, offAgain.Add(offRouteSettings.offRouteAfter+time.Second))
	require.Len(t, got, 1)
	assert.Equal(t, offAgain, got[0].Since)
}

// TestAlerts_OffRouteClearsWhenTripEnds: the alert goes with its trip, and
// the vehicle's next trip, even on the same GTFS trip, starts from nothing,
// whether or not the alerts were worked out in between to see the end.
func TestAlerts_OffRouteClearsWhenTripEnds(t *testing.T) {
	for _, seen := range []bool{true, false} {
		t.Run(fmt.Sprintf("end seen by the alerts %v", seen), func(t *testing.T) {
			w := newJoinedWatcher(plainFixtureIndex(t))
			firstOff := alertT0.Add(time.Minute)
			raised := firstOff.Add(offRouteSettings.offRouteAfter + 5*time.Second)
			north := driveT1(w, firstOff, raised, 50, 200, nil)
			require.Len(t, w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, raised), 1)

			ended := raised.Add(5 * time.Second)
			if seen {
				assert.Empty(t, w.alerts(nil, ended), "an ended trip has no alert")
				assert.NotContains(t, w.routes, "bus-7", "nor anything kept for it")
			}

			// The vehicle starts the next run of the same GTFS trip where it is,
			// off the shape, and stays off it past the threshold, counted from
			// either the trip's start or its first report.
			next := alertTrip("bus-7", ended.Add(time.Minute))
			next.ID = 8
			firstReport := next.StartTime.Add(time.Minute)
			assert.Empty(t, w.alerts([]TripSummary{next}, firstReport), "no report on this trip yet")
			last := firstReport.Add(offRouteSettings.offRouteAfter + time.Minute)
			for at := firstReport; !at.After(last); at, north = at.Add(5*time.Second), north+2 {
				w.report(onRun(routeFix("T1", alongT1(north, 200), nil), next.StartTime), at)
			}
			assert.Empty(t, w.alerts([]TripSummary{next}, last),
				"the last trip's time off route does not count towards this one")
		})
	}
}

// TestAlerts_OffRouteOnlyAfterJoiningTheRoute: a trip can go off its route
// only once it has been on it. A driver who starts the trip at a layover spot
// or a depot away from the shape is on the way to the route, not off it, and
// one who waits there shows as not moving instead.
func TestAlerts_OffRouteOnlyAfterJoiningTheRoute(t *testing.T) {
	ix := plainFixtureIndex(t)
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	after := offRouteSettings.offRouteAfter

	waits := []struct {
		name    string
		layover rider.LatLon
	}{
		{"beside the first stop", alongT1(0, 200)},
		{"behind the first stop, past the end of the shape", alongT1(-150, 0)},
	}
	for _, tc := range waits {
		t.Run("waiting "+tc.name, func(t *testing.T) {
			w := newRouteWatcher(ix)
			report := func(from, to time.Time) {
				for at := from; !at.After(to); at = at.Add(5 * time.Second) {
					w.report(routeFix("T1", tc.layover, nil), at)
				}
			}
			first := alertT0.Add(5 * time.Second)
			pastOffRoute := first.Add(after + time.Minute)
			report(first, pastOffRoute)
			assert.Empty(t, w.alerts(trips, pastOffRoute))

			pastStopped := first.Add(offRouteSettings.stoppedAfter + time.Minute)
			report(pastOffRoute.Add(5*time.Second), pastStopped)
			got := w.alerts(trips, pastStopped)
			require.Len(t, got, 1)
			assert.Equal(t, alertNotMoving, got[0].Kind)
		})
	}

	t.Run("driving from the depot", func(t *testing.T) {
		w := newRouteWatcher(ix)
		reached := alertT0.Add(after + time.Minute)
		north := driveT1(w, alertT0.Add(5*time.Second), reached, 0, 200, nil)
		assert.Empty(t, w.alerts(trips, reached), "on its way to the route")

		w.report(routeFix("T1", alongT1(north, 5), nil), reached.Add(5*time.Second))
		firstOff := reached.Add(10 * time.Second)
		last := firstOff.Add(after + 5*time.Second)
		driveT1(w, firstOff, last, north+2, 200, nil)
		got := w.alerts(trips, last)
		require.Len(t, got, 1, "off route once it has been on it")
		assert.Equal(t, alertOffRoute, got[0].Kind)
		assert.Equal(t, firstOff, got[0].Since)
	})
}

// TestAlerts_OffRouteGoesQuiet: a vehicle that stops reporting while off
// route shows as quiet instead, since nothing says where it is now.
func TestAlerts_OffRouteGoesQuiet(t *testing.T) {
	w := newJoinedWatcher(plainFixtureIndex(t))
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	firstOff := alertT0.Add(time.Minute)
	raised := firstOff.Add(offRouteSettings.offRouteAfter + 5*time.Second)
	driveT1(w, firstOff, raised, 50, 200, nil)
	require.Len(t, w.alerts(trips, raised), 1)

	got := w.alerts(trips, raised.Add(offRouteSettings.quietAfter+time.Second))
	require.Len(t, got, 1)
	assert.Equal(t, alertQuiet, got[0].Kind)
	assert.Equal(t, raised, got[0].Since)
}

// TestAlerts_OffRouteOutranksNotMoving: a vehicle standing still away from
// its route is shown as off route, so its alert does not change kind when it
// has stood there long enough to be not moving as well.
func TestAlerts_OffRouteOutranksNotMoving(t *testing.T) {
	w := newJoinedWatcher(plainFixtureIndex(t))
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	firstOff := alertT0.Add(time.Minute)
	parked := alongT1(500, 200)
	last := firstOff.Add(offRouteSettings.stoppedAfter + time.Minute)
	for at := firstOff; !at.After(last); at = at.Add(5 * time.Second) {
		w.report(routeFix("T1", parked, nil), at)
	}

	got := w.alerts(trips, last)
	require.Len(t, got, 1)
	assert.Equal(t, alertOffRoute, got[0].Kind)
	assert.Equal(t, firstOff, got[0].Since)
}

// TestAlerts_OffRouteNeedsAShape: only a trip whose shape the schedule gives
// can be off route. Headway trips have one like any other.
func TestAlerts_OffRouteNeedsAShape(t *testing.T) {
	plain := plainFixtureIndex(t)
	headway := frequencyFixtureIndex(t)
	require.True(t, mustTrip(t, headway, "T1").FrequencyBased)
	require.True(t, mustTrip(t, plain, "T4").ShapeFromStops)

	tests := []struct {
		name   string
		ix     *rider.Index
		tripID string
		want   bool
	}{
		{"timetabled trip", plain, "T1", true},
		{"headway trip", headway, "T1", true},
		{"shape drawn through its stops", plain, "T4", false},
		{"route-only trip", plain, "", false},
		{"trip not in the schedule", plain, "T9", false},
		{"no schedule loaded", nil, "T1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newRouteWatcher(tc.ix)
			trip := alertTrip("bus-7", alertT0)
			trip.GtfsTripID = tc.tripID
			w.report(routeFix(tc.tripID, alongT1(10, 3), nil), alertT0.Add(30*time.Second))
			firstOff := alertT0.Add(time.Minute)
			lastOff := firstOff.Add(offRouteSettings.offRouteAfter + time.Minute)
			for at, north := firstOff, 50.0; !at.After(lastOff); at, north = at.Add(5*time.Second), north+2 {
				w.report(routeFix(tc.tripID, alongT1(north, 200), nil), at)
			}

			got := w.alerts([]TripSummary{trip}, lastOff)

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertOffRoute, got[0].Kind)
		})
	}

	t.Run("watcher never given a schedule", func(t *testing.T) {
		w := newTestWatcher()
		w.settings = offRouteSettings
		firstOff := alertT0.Add(time.Minute)
		lastOff := firstOff.Add(offRouteSettings.offRouteAfter + time.Minute)
		driveT1(w, firstOff, lastOff, 50, 200, nil)
		assert.Empty(t, w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, lastOff))
	})
}

// TestAlerts_OffRoutePoorAccuracy: a fix counts only when its accuracy circle
// lies wholly on one side of the distance, so a poor one neither raises the
// alert nor clears it.
func TestAlerts_OffRoutePoorAccuracy(t *testing.T) {
	ix := plainFixtureIndex(t)
	trips := []TripSummary{alertTrip("bus-7", alertT0)}
	firstOff := alertT0.Add(time.Minute)
	lastOff := firstOff.Add(offRouteSettings.offRouteAfter + time.Minute)

	t.Run("does not raise it", func(t *testing.T) {
		w := newJoinedWatcher(ix)
		driveT1(w, firstOff, lastOff, 50, 200, metres(150))
		assert.Empty(t, w.alerts(trips, lastOff), "200 m out, but within 60 m plus its 150 m")
	})

	t.Run("the same fixes, accurate enough, raise it", func(t *testing.T) {
		w := newJoinedWatcher(ix)
		driveT1(w, firstOff, lastOff, 50, 200, metres(130))
		got := w.alerts(trips, lastOff)
		require.Len(t, got, 1)
		assert.Equal(t, alertOffRoute, got[0].Kind)
	})

	t.Run("does not clear it", func(t *testing.T) {
		w := newJoinedWatcher(ix)
		north := driveT1(w, firstOff, lastOff, 50, 200, nil)
		require.Len(t, w.alerts(trips, lastOff), 1)

		poor := lastOff.Add(5 * time.Second)
		w.report(routeFix("T1", alongT1(north, 5), metres(100)), poor)
		got := w.alerts(trips, poor)
		require.Len(t, got, 1, "5 m from the shape, but 100 m either way")
		assert.Equal(t, alertOffRoute, got[0].Kind)
		assert.Equal(t, firstOff, got[0].Since)
	})
}

func TestJudgeDistance(t *testing.T) {
	const limit = 60.0
	tests := []struct {
		name     string
		distance float64
		accuracy float64
		want     shapeSide
	}{
		{"on the shape", 0, 0, sideNear},
		{"at the distance", 60, 0, sideNear},
		{"just past it", 60.5, 0, sideOff},
		{"at the distance plus accuracy", 80, 20, sideUnknown},
		{"just past the distance plus accuracy", 80.5, 20, sideOff},
		{"accuracy reaches the distance exactly", 40, 20, sideNear},
		{"accuracy reaches just past it", 40.5, 20, sideUnknown},
		{"negative accuracy counts as none", 50, -50, sideNear},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, judgeDistance(tc.distance, tc.accuracy, limit))
		})
	}
}

func TestAlertSettingsFromEnv(t *testing.T) {
	const maxShapeDistance = 75.0

	t.Run("defaults", func(t *testing.T) {
		for _, key := range []string{"ALERT_QUIET_AFTER", "ALERT_STOPPED_AFTER", "ALERT_OFF_ROUTE_DISTANCE", "ALERT_OFF_ROUTE_AFTER"} {
			t.Setenv(key, "")
		}
		assert.Equal(t, alertSettings{
			quietAfter:       defaultAlertQuietAfter,
			stoppedAfter:     defaultAlertStoppedAfter,
			offRouteDistance: maxShapeDistance,
			offRouteAfter:    defaultAlertOffRouteAfter,
			notStartedAfter:  defaultAlertNotStartedAfter,
		}, alertSettingsFromEnv(maxShapeDistance))
	})

	distances := []struct {
		value string
		want  float64
	}{
		{"120", 120},
		{"42.5", 42.5},
		{"0", maxShapeDistance},
		{"-10", maxShapeDistance},
		{"NaN", maxShapeDistance},
		{"Inf", maxShapeDistance},
		{"far", maxShapeDistance},
	}
	for _, tc := range distances {
		t.Run("ALERT_OFF_ROUTE_DISTANCE="+tc.value, func(t *testing.T) {
			t.Setenv("ALERT_OFF_ROUTE_DISTANCE", tc.value)
			assert.Equal(t, tc.want, alertSettingsFromEnv(maxShapeDistance).offRouteDistance)
		})
	}

	durations := []struct {
		value string
		want  time.Duration
	}{
		{"2m", 2 * time.Minute},
		{"90s", 90 * time.Second},
		{"0s", defaultAlertOffRouteAfter},
		{"-1m", defaultAlertOffRouteAfter},
		{"soon", defaultAlertOffRouteAfter},
	}
	for _, tc := range durations {
		t.Run("ALERT_OFF_ROUTE_AFTER="+tc.value, func(t *testing.T) {
			t.Setenv("ALERT_OFF_ROUTE_AFTER", tc.value)
			assert.Equal(t, tc.want, alertSettingsFromEnv(maxShapeDistance).offRouteAfter)
		})
	}

	t.Run("the existing two still read", func(t *testing.T) {
		t.Setenv("ALERT_QUIET_AFTER", "4m")
		t.Setenv("ALERT_STOPPED_AFTER", "15m")
		got := alertSettingsFromEnv(maxShapeDistance)
		assert.Equal(t, 4*time.Minute, got.quietAfter)
		assert.Equal(t, 15*time.Minute, got.stoppedAfter)
	})
}

// startedAtT0Store authorizes every report as on a trip that started at
// alertT0, as Store.AuthorizeLocation sets it from the trips row.
type startedAtT0Store struct{ noopStore }

func (s *startedAtT0Store) AuthorizeLocation(_ context.Context, loc *LocationReport) error {
	loc.TripStartedAt = alertT0
	return nil
}

// TestLocationRoute_FeedsOffRoute drives a report through the real mux: the
// location route must hand the whole report to the watcher, trip, its start
// and accuracy included, or no vehicle is ever off route.
func TestLocationRoute_FeedsOffRoute(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	// Three reports in a row, more than a driver may send at once.
	limiter := NewKeyedRateLimiter(time.Millisecond, 3, false)
	defer limiter.Stop()
	alerts := newRouteWatcher(plainFixtureIndex(t))
	mux := newMux(&startedAtT0Store{}, tracker, limiter, testSecret, testTTLs, time.Time{}, nil, nil, false, false, "", nil, nil, nil, alerts, nil, newFeedHealth(time.Now))
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)

	post := func(pos rider.LatLon, accuracy float64) {
		t.Helper()
		body := fmt.Sprintf(`{"vehicle_id":"bus-7","trip_id":"T1","route_id":"R1","latitude":%f,"longitude":%f,"accuracy":%g,"timestamp":%d}`,
			pos.Lat, pos.Lon, accuracy, time.Now().Unix())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+driverToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}

	post(alongT1(100, 3), 5)
	require.Equal(t, alertT0, alerts.routes["bus-7"].tripStart, "joined the route of the trip AuthorizeLocation found")

	post(alongT1(100, 200), 150)
	assert.True(t, alerts.routes["bus-7"].offSince.IsZero(), "200 m out with 150 m accuracy is not off route")

	post(alongT1(100, 200), 10)
	assert.False(t, alerts.routes["bus-7"].offSince.IsZero())
}

// TestAlerts_OffRouteFromStore runs the off-route alert over a real trip: the
// trip start AuthorizeLocation puts on each report must be the one ListTrips
// gives the trip, or no trip is ever off route.
func TestAlerts_OffRouteFromStore(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "R1", "T1")
	require.NoError(t, err)
	w := newAlertWatcher(store, offRouteSettings)
	w.watchingSince = trip.StartTime
	ix := plainFixtureIndex(t)
	w.watchRoutes(func() *rider.Index { return ix })
	report := func(pos rider.LatLon, at time.Time) {
		t.Helper()
		loc := &LocationReport{DriverID: strconv.FormatInt(userID, 10), VehicleID: "bus-trip-1", TripID: "T1", RouteID: "R1", Latitude: pos.Lat, Longitude: pos.Lon}
		require.NoError(t, store.AuthorizeLocation(ctx, loc))
		w.report(loc, at)
	}

	// The database and the test can disagree on the clock by a little, so
	// everything is timed from the trip's own start.
	report(alongT1(10, 3), trip.StartTime.Add(10*time.Second))
	firstOff := trip.StartTime.Add(20 * time.Second)
	report(alongT1(100, 200), firstOff)
	lastOff := firstOff.Add(offRouteSettings.offRouteAfter + time.Second)
	report(alongT1(400, 200), lastOff)

	got, err := w.list(ctx, lastOff)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, alertOffRoute, got[0].Kind)
	assert.Equal(t, trip.ID, got[0].Trip.ID)
	assert.Equal(t, firstOff.UTC(), got[0].Since)

	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)
	got, err = w.list(ctx, lastOff)
	require.NoError(t, err)
	assert.Empty(t, got, "an ended trip has no alerts")
}

// TestAlerts_OffRouteConcurrent runs reports and lists side by side, for the
// race detector.
func TestAlerts_OffRouteConcurrent(t *testing.T) {
	w := newRouteWatcher(plainFixtureIndex(t))
	var trips []TripSummary
	for i := range 4 {
		trips = append(trips, alertTrip("bus-"+strconv.Itoa(i), alertT0))
	}

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 200 {
				east := 200.0
				if n%10 == 0 {
					east = 5
				}
				// A new run of the trip every 50 reports.
				run := alertT0.Add(time.Duration(n/50) * time.Hour)
				pos := alongT1(float64(n), east)
				w.report(&LocationReport{VehicleID: "bus-" + strconv.Itoa(i), TripID: "T1", Latitude: pos.Lat, Longitude: pos.Lon, TripStartedAt: run},
					alertT0.Add(time.Duration(n)*5*time.Second))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := range 200 {
			w.alerts(trips, alertT0.Add(time.Duration(n)*5*time.Second))
		}
	}()
	wg.Wait()
}

// TestOpenAPI_AlertKinds keeps the spec's list of alert kinds in step with the
// kinds the watcher returns.
func TestOpenAPI_AlertKinds(t *testing.T) {
	spec := loadOpenAPISpec(t)
	schemas := spec.document["components"].(map[string]any)["schemas"].(map[string]any)
	kind := schemas["Alert"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
	assert.ElementsMatch(t, []any{alertQuiet, alertNotMoving, alertOffRoute, alertNotStarted}, kind["enum"])
}
