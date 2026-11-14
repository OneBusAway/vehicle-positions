package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAlertSettings are main's defaults, for the tests that wire a mux or the
// admin UI.
var testAlertSettings = alertSettings{quietAfter: defaultAlertQuietAfter, stoppedAfter: defaultAlertStoppedAfter}

// alertT0 is when the alert tests' trips start.
var alertT0 = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// alertStop is where the alert tests' vehicles stand still.
var alertStop = rider.LatLon{Lat: -1.2921, Lon: 36.8219}

// newTestWatcher is a watcher that started an hour before alertT0, so that
// only the tests about a restart see its start.
func newTestWatcher() *alertWatcher {
	w := newAlertWatcher(&noopStore{}, testAlertSettings)
	w.watchingSince = alertT0.Add(-time.Hour)
	return w
}

func alertTrip(vehicleID string, start time.Time) TripSummary {
	return TripSummary{
		ID:           7,
		VehicleID:    vehicleID,
		VehicleLabel: "Bus 7",
		UserID:       3,
		DriverName:   "Achieng",
		RouteID:      "R1",
		GtfsTripID:   "T1",
		StartTime:    start,
		Status:       "active",
	}
}

// reportEvery records a report from pos every interval from from to to,
// both included.
func reportEvery(w *alertWatcher, vehicleID string, pos rider.LatLon, from, to time.Time, interval time.Duration) {
	for at := from; !at.After(to); at = at.Add(interval) {
		w.record(vehicleID, pos, at)
	}
}

// metresNorth is a point the given distance north of alertStop. OffsetMetres
// and rider.Distance use different Earth models, so a distance used at a
// threshold must stay a metre or so clear of it.
func metresNorth(metres float64) rider.LatLon {
	return rider.OffsetMetres(alertStop, metres, 0)
}

func TestAlerts_GoneQuiet(t *testing.T) {
	quietAfter := testAlertSettings.quietAfter
	lastReport := alertT0.Add(time.Minute)

	tests := []struct {
		name  string
		after time.Duration
		want  bool
	}{
		{"just under", quietAfter - time.Second, false},
		{"exactly at", quietAfter, false},
		{"just over", quietAfter + time.Second, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWatcher()
			trip := alertTrip("bus-7", alertT0)
			w.record("bus-7", alertStop, lastReport)

			got := w.alerts([]TripSummary{trip}, lastReport.Add(tc.after))

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertQuiet, got[0].Kind)
			assert.Equal(t, lastReport, got[0].Since)
			assert.Equal(t, int64(tc.after/time.Second), got[0].DurationSeconds)
			assert.Equal(t, trip, got[0].Trip)
		})
	}
}

// TestAlerts_GoneQuietWithNoReportOnTheTrip pins where a vehicle that has not
// reported on its trip is counted from: the trip's start, and not a report
// from its last trip, or the watcher's start when the server restarted
// mid-trip.
func TestAlerts_GoneQuietWithNoReportOnTheTrip(t *testing.T) {
	quietAfter := testAlertSettings.quietAfter
	restart := alertT0.Add(20 * time.Minute)

	tests := []struct {
		name          string
		watchingSince time.Time
		lastTrip      bool
		wantSince     time.Time
	}{
		{"no report at all", alertT0.Add(-time.Hour), false, alertT0},
		{"only a report from the last trip", alertT0.Add(-time.Hour), true, alertT0},
		{"server restarted mid-trip", restart, false, restart},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWatcher()
			w.watchingSince = tc.watchingSince
			if tc.lastTrip {
				w.record("bus-7", alertStop, alertT0.Add(-time.Minute))
			}
			trips := []TripSummary{alertTrip("bus-7", alertT0)}

			assert.Empty(t, w.alerts(trips, tc.wantSince.Add(quietAfter-time.Second)))

			got := w.alerts(trips, tc.wantSince.Add(quietAfter+time.Second))
			require.Len(t, got, 1)
			assert.Equal(t, alertQuiet, got[0].Kind)
			assert.Equal(t, tc.wantSince, got[0].Since)
		})
	}
}

func TestAlerts_NotMoving(t *testing.T) {
	stoppedAfter := testAlertSettings.stoppedAfter
	firstReport := alertT0.Add(10 * time.Second)

	tests := []struct {
		name  string
		after time.Duration
		want  bool
	}{
		{"just under", stoppedAfter - time.Second, false},
		{"exactly at", stoppedAfter, true},
		{"just over", stoppedAfter + time.Second, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWatcher()
			trip := alertTrip("bus-7", alertT0)
			now := firstReport.Add(tc.after)
			reportEvery(w, "bus-7", alertStop, firstReport, now, 5*time.Second)

			got := w.alerts([]TripSummary{trip}, now)

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertNotMoving, got[0].Kind)
			assert.Equal(t, firstReport, got[0].Since)
			assert.Equal(t, int64(tc.after/time.Second), got[0].DurationSeconds)
			assert.Equal(t, trip, got[0].Trip)
		})
	}
}

// TestAlerts_NotMovingRadius moves the vehicle partway through its stop: a
// move short of stoppedRadius is still the same stop, and one past it starts a
// new one, which has not lasted long enough yet. The short stoppedAfter keeps
// the whole stop in the history, so only the radius decides where it began.
func TestAlerts_NotMovingRadius(t *testing.T) {
	stoppedAfter := 3 * time.Minute
	moveAt := alertT0.Add(4 * time.Minute)
	now := moveAt.Add(time.Minute)

	tests := []struct {
		name   string
		metres float64
		want   bool
	}{
		{"moved just under the radius", stoppedRadius - 1, true},
		{"moved just over the radius", stoppedRadius + 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWatcher()
			w.settings.stoppedAfter = stoppedAfter
			reportEvery(w, "bus-7", alertStop, alertT0, moveAt.Add(-5*time.Second), 5*time.Second)
			reportEvery(w, "bus-7", metresNorth(tc.metres), moveAt, now, 5*time.Second)

			got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, now)

			if !tc.want {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, alertNotMoving, got[0].Kind)
			assert.Equal(t, alertT0, got[0].Since)
		})
	}
}

// TestAlerts_QuietIsNotAlsoNotMoving: a vehicle that stood still and then went
// quiet has one alert, the quiet one. Its last position says nothing about
// where it is now.
func TestAlerts_QuietIsNotAlsoNotMoving(t *testing.T) {
	w := newTestWatcher()
	lastReport := alertT0.Add(20 * time.Minute)
	reportEvery(w, "bus-7", alertStop, alertT0, lastReport, 5*time.Second)

	got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, lastReport.Add(testAlertSettings.quietAfter+time.Second))

	require.Len(t, got, 1)
	assert.Equal(t, alertQuiet, got[0].Kind)
}

func TestAlerts_TripEndClearsAlerts(t *testing.T) {
	w := newTestWatcher()
	now := alertT0.Add(time.Hour)
	reportEvery(w, "bus-7", alertStop, alertT0, now, 5*time.Second)
	require.Len(t, w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, now), 1, "the vehicle has been standing still for an hour")

	assert.Empty(t, w.alerts(nil, now), "the trip has ended")
	assert.NotContains(t, w.history, "bus-7", "an ended trip's reports must go with it")
}

// TestAlerts_NewTripIgnoresTheLastTripsReports covers a vehicle that ends one
// trip and starts the next where it stands, before alerts were worked out in
// between to clear its history: the wait before the new trip is not part of
// it.
func TestAlerts_NewTripIgnoresTheLastTripsReports(t *testing.T) {
	w := newTestWatcher()
	secondTrip := alertT0.Add(time.Hour)
	reportEvery(w, "bus-7", alertStop, alertT0, secondTrip.Add(-time.Second), 5*time.Second)
	now := secondTrip.Add(2 * time.Minute)
	reportEvery(w, "bus-7", alertStop, secondTrip, now, 5*time.Second)

	assert.Empty(t, w.alerts([]TripSummary{alertTrip("bus-7", secondTrip)}, now))
}

func TestAlerts_HistoryStaysBounded(t *testing.T) {
	t.Run("a moving vehicle", func(t *testing.T) {
		w := newTestWatcher()
		for i := range 2000 {
			w.record("bus-7", metresNorth(float64(i)*100), alertT0.Add(time.Duration(i)*5*time.Second))
		}
		samples := w.history["bus-7"]
		assert.Len(t, samples, positionHistorySize)
		assert.Equal(t, alertT0.Add(1999*5*time.Second), samples[len(samples)-1].at, "the newest sample is the latest report")
	})

	// The history holds about eight minutes, and the stop has lasted two hours:
	// its start must still be in there for the alert to say so.
	t.Run("a vehicle standing still keeps the start of its stop", func(t *testing.T) {
		w := newTestWatcher()
		now := alertT0.Add(2 * time.Hour)
		reportEvery(w, "bus-7", alertStop, alertT0, now, 5*time.Second)

		assert.Len(t, w.history["bus-7"], positionHistorySize)
		got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, now)
		require.Len(t, got, 1)
		assert.Equal(t, alertNotMoving, got[0].Kind)
		assert.Equal(t, alertT0, got[0].Since)
	})
}

// TestAlerts_StopOutlastsDrift creeps a stopped vehicle 30 m, and 30 m again,
// so the start of its stop ends up out of range. The stop is then counted from
// the oldest sample still in range, which a history spanning minutes rather
// than the last sixteen reports still holds.
func TestAlerts_StopOutlastsDrift(t *testing.T) {
	w := newTestWatcher()
	every := 5 * time.Second
	reportEvery(w, "bus-7", alertStop, alertT0, alertT0.Add(5*time.Minute-every), every)
	reportEvery(w, "bus-7", metresNorth(30), alertT0.Add(5*time.Minute), alertT0.Add(13*time.Minute-every), every)
	now := alertT0.Add(19 * time.Minute)
	reportEvery(w, "bus-7", metresNorth(60), alertT0.Add(13*time.Minute), now, every)

	got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, now)

	require.Len(t, got, 1)
	assert.Equal(t, alertNotMoving, got[0].Kind)
	assert.False(t, got[0].Since.Before(alertT0.Add(5*time.Minute)), "the stop no longer reaches back to where it started")
}

// TestAlerts_LateReportIsDropped covers two reports from one vehicle racing:
// the one accepted second but taken first must not become its latest.
func TestAlerts_LateReportIsDropped(t *testing.T) {
	w := newTestWatcher()
	latest := alertT0.Add(20 * time.Second)
	w.record("bus-7", alertStop, latest)
	w.record("bus-7", alertStop, alertT0.Add(10*time.Second))

	got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, latest.Add(testAlertSettings.quietAfter+time.Second))

	require.Len(t, got, 1)
	assert.Equal(t, latest, got[0].Since)
}

func TestAlerts_LongestRunningFirst(t *testing.T) {
	w := newTestWatcher()
	now := alertT0.Add(time.Hour)
	w.record("bus-c", alertStop, now.Add(-10*time.Minute))
	w.record("bus-b", alertStop, now.Add(-20*time.Minute))
	w.record("bus-a", alertStop, now.Add(-10*time.Minute))
	trips := []TripSummary{alertTrip("bus-c", alertT0), alertTrip("bus-b", alertT0), alertTrip("bus-a", alertT0)}

	got := w.alerts(trips, now)

	require.Len(t, got, 3)
	assert.Equal(t, "bus-b", got[0].Trip.VehicleID, "the longest-running alert comes first")
	assert.Equal(t, "bus-a", got[1].Trip.VehicleID, "a tie goes by vehicle id")
	assert.Equal(t, "bus-c", got[2].Trip.VehicleID)
}

func TestAlertFeed_RecordsOnlySavedReports(t *testing.T) {
	loc := &LocationReport{VehicleID: "bus-7", Latitude: alertStop.Lat, Longitude: alertStop.Lon, Timestamp: time.Now().Unix()}

	t.Run("saved", func(t *testing.T) {
		w := newAlertWatcher(&noopStore{}, testAlertSettings)
		before := time.Now()
		require.NoError(t, alertFeed{LocationStore: &mockStore{}, alerts: w}.SaveLocation(context.Background(), loc))

		samples := w.history["bus-7"]
		require.Len(t, samples, 1)
		assert.Equal(t, alertStop, samples[0].pos)
		assert.False(t, samples[0].at.Before(before), "a report is recorded when the server accepts it")
	})

	t.Run("not saved", func(t *testing.T) {
		w := newAlertWatcher(&noopStore{}, testAlertSettings)
		saveErr := errors.New("database down")
		err := alertFeed{LocationStore: &mockStore{err: saveErr}, alerts: w}.SaveLocation(context.Background(), loc)

		assert.ErrorIs(t, err, saveErr)
		assert.Empty(t, w.history)
	})
}

// TestAlerts_ListReadsEveryActiveTrip pins the read: active trips only, and
// all of them, since a page of them would leave the rest unchecked.
func TestAlerts_ListReadsEveryActiveTrip(t *testing.T) {
	trips := &fakeTripLister{trips: []TripSummary{alertTrip("bus-7", alertT0)}}
	w := newAlertWatcher(trips, testAlertSettings)

	got, err := w.list(context.Background(), alertT0.Add(time.Hour))

	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, TripFilter{Status: "active", Limit: math.MaxInt32}, trips.captured)

	_, err = newAlertWatcher(&fakeTripLister{err: errors.New("database down")}, testAlertSettings).list(context.Background(), alertT0)
	assert.ErrorContains(t, err, "list active trips: database down")
}

func TestHandleListAlerts(t *testing.T) {
	getAlerts := func(t *testing.T, w *alertWatcher) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		handleListAlerts(w)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/alerts", nil))
		return rec
	}

	t.Run("empty", func(t *testing.T) {
		rec := getAlerts(t, newAlertWatcher(&fakeTripLister{}, testAlertSettings))

		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"count":0,"alerts":[]}`, rec.Body.String())
	})

	t.Run("each alert", func(t *testing.T) {
		now := time.Now()
		start := now.Add(-time.Hour)
		trips := &fakeTripLister{}
		w := newAlertWatcher(trips, testAlertSettings)
		w.watchingSince = start
		w.record("bus-quiet", alertStop, now.Add(-5*time.Minute))
		reportEvery(w, "bus-still", alertStop, now.Add(-12*time.Minute), now, 5*time.Second)
		quiet := alertTrip("bus-quiet", start)
		still := alertTrip("bus-still", start)
		still.ID = 8
		trips.trips = []TripSummary{quiet, still}

		rec := getAlerts(t, w)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp struct {
			Count  int `json:"count"`
			Alerts []struct {
				Kind            string          `json:"kind"`
				Since           time.Time       `json:"since"`
				DurationSeconds int64           `json:"duration_seconds"`
				Trip            json.RawMessage `json:"trip"`
			} `json:"alerts"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Equal(t, 2, resp.Count)
		require.Len(t, resp.Alerts, 2)

		notMoving, goneQuiet := resp.Alerts[0], resp.Alerts[1]
		assert.Equal(t, alertNotMoving, notMoving.Kind)
		assert.WithinDuration(t, now.Add(-12*time.Minute), notMoving.Since, time.Second)
		assert.InDelta(t, 12*60, notMoving.DurationSeconds, 1)
		assert.Equal(t, alertQuiet, goneQuiet.Kind)
		assert.WithinDuration(t, now.Add(-5*time.Minute), goneQuiet.Since, time.Second)
		assert.InDelta(t, 5*60, goneQuiet.DurationSeconds, 1)

		var trip map[string]any
		require.NoError(t, json.Unmarshal(goneQuiet.Trip, &trip))
		assert.Equal(t, "bus-quiet", trip["vehicle_id"])
		assert.Equal(t, "Bus 7", trip["vehicle_label"])
		assert.Equal(t, "Achieng", trip["driver_name"])
		assert.Equal(t, "R1", trip["route_id"])
		assert.Equal(t, "T1", trip["gtfs_trip_id"])
		assert.EqualValues(t, 7, trip["id"])
	})

	t.Run("store error", func(t *testing.T) {
		logs := captureLogs(t)
		rec := getAlerts(t, newAlertWatcher(&fakeTripLister{err: errors.New("database down")}, testAlertSettings))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "failed to list alerts", decodeError(t, rec))
		assert.Contains(t, logs.String(), "database down")
	})
}

// TestLocationRoute_FeedsAlerts drives a report through the real mux: the
// location route must save through alertFeed, or the watcher never hears of
// a vehicle and no vehicle is ever not moving.
func TestLocationRoute_FeedsAlerts(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	limiter := NewVehicleRateLimiter()
	defer limiter.Stop()
	alerts := newAlertWatcher(&noopStore{}, testAlertSettings)
	mux := newMux(&noopStore{}, tracker, limiter, testSecret, testTTLs, time.Time{}, nil, nil, false, false, nil, nil, nil, alerts)
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)

	body := `{"vehicle_id":"bus-7","latitude":-1.2921,"longitude":36.8219,"timestamp":` + strconv.FormatInt(time.Now().Unix(), 10) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+driverToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Len(t, alerts.history["bus-7"], 1)
}

// TestAlerts_FromStore runs the alerts over real active trips: the trip
// fields come from the database, and ending the trip clears its alert.
func TestAlerts_FromStore(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "R1", "T1")
	require.NoError(t, err)
	w := newAlertWatcher(store, testAlertSettings)
	// The database and the test can disagree on the clock by a little, so
	// everything is timed from the trip's own start.
	w.watchingSince = trip.StartTime

	got, err := w.list(ctx, trip.StartTime.Add(testAlertSettings.quietAfter-time.Second))
	require.NoError(t, err)
	assert.Empty(t, got, "a trip just started is not quiet yet")

	quietAt := trip.StartTime.Add(testAlertSettings.quietAfter + time.Second)
	got, err = w.list(ctx, quietAt)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, alertQuiet, got[0].Kind)
	assert.Equal(t, trip.ID, got[0].Trip.ID)
	assert.Equal(t, "bus-trip-1", got[0].Trip.VehicleID)
	assert.Equal(t, "Bus 1", got[0].Trip.VehicleLabel)
	assert.Equal(t, "Trip Driver", got[0].Trip.DriverName)
	assert.Equal(t, "R1", got[0].Trip.RouteID)
	assert.Equal(t, "T1", got[0].Trip.GtfsTripID)

	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)
	got, err = w.list(ctx, quietAt)
	require.NoError(t, err)
	assert.Empty(t, got, "an ended trip has no alerts")
}
