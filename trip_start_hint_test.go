package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// startNextTrip ends bus-1's trip and starts its next one, tripID, through the
// handlers, as a driver app does between two runs of a block.
func startNextTrip(t *testing.T, tracker *Tracker, tripID string) {
	t.Helper()
	end := handleEndTrip(&mockTripEnder{vehicleID: "bus-1"}, tracker)
	require.Equal(t, http.StatusOK, tripRequest(t, end, "42", EndTripRequest{TripID: 1}).Code)
	require.Empty(t, tracker.ActiveVehicles(), "ending the trip takes the vehicle out of the feed")

	starter := &mockTripStarter{trip: &TripResponse{ID: 2, UserID: 42, VehicleID: "bus-1", GtfsTripID: tripID, Status: "active"}}
	start := handleStartTrip(starter, nil, tracker)
	require.Equal(t, http.StatusCreated, tripRequest(t, start, "42", StartTripRequest{VehicleID: "bus-1", GtfsTripID: tripID}).Code)
}

// On T3's loop the terminal is both the first and the last stop. Ending a run
// takes the vehicle out of the tracker, so the next run's first fix there has
// no place to start from; nearer the closing pass, it would be STOPPED_AT the
// last stop, and the hint would keep it there through the layover.
func TestTracker_NewTripAfterEndTripStartsAtItsFirstStop(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t3 := mustTrip(t, ix, "T3")
	tracker := newPlacingTracker(t, ix)

	reportOn(t, tracker, "T3", "20260902", t3.Shape.PointAt(t3.Shape.Length-30))
	finished := reportOn(t, tracker, "T3", "20260902", loopClosingCorner(t3))
	require.Equal(t, 4, finished.StopSequence, "the run ends at its last stop")

	startNextTrip(t, tracker, "T5")

	started := reportOn(t, tracker, "T5", "20260902", loopOpeningCorner(t3))
	assert.Equal(t, "LP1", started.StopID)
	assert.Equal(t, 1, started.StopSequence, "a new trip starts at its first stop, not its last")
	assert.True(t, started.StoppedAt)

	// Only the first report starts from the start of the shape: after it the
	// vehicle's own place is the hint, and it drives the loop round to the
	// last stop.
	sequences := []int{reportOn(t, tracker, "T5", "20260902", loopClosingCorner(t3)).StopSequence}
	for _, along := range []float64{300, 700, 1100, 1500, 1800, 1950} {
		sequences = append(sequences, reportOn(t, tracker, "T5", "20260902", t3.Shape.PointAt(along)).StopSequence)
	}
	sequences = append(sequences, reportOn(t, tracker, "T5", "20260902", loopClosingCorner(t3)).StopSequence)
	assert.Equal(t, []int{1, 2, 2, 3, 3, 4, 4, 4}, sequences)
}

// The same on T6, an out-and-back whose return leg ends 15 m east of where the
// outbound leg starts. The next run's first fix at the terminal, 9 m east of
// ST1, is nearer the return leg's end: without the start of the shape as its
// hint, the vehicle was put at RT1, the trip's last stop, and kept on the
// return leg while it drove the outbound one.
func TestTracker_NewOutAndBackRunAfterEndTripStaysOutbound(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t6 := mustTrip(t, ix, "T6")
	tracker := newPlacingTracker(t, ix)

	finished := reportOn(t, tracker, "T6", "20260902", t6.Shape.PointAt(t6.Shape.Length))
	require.Equal(t, "RT1", finished.StopID)

	startNextTrip(t, tracker, "T6")

	var got []string
	for _, pos := range []rider.LatLon{
		rider.OffsetMetres(t6.Shape.Points[0], 0, 9),
		t6.Shape.PointAt(100),
		t6.Shape.PointAt(300),
	} {
		got = append(got, reportOn(t, tracker, "T6", "20260903", pos).StopID)
	}
	assert.Equal(t, []string{"ST1", "ST2", "ST2"}, got, "the next run starts outbound, from its first stop")
}

// A start the store refuses, here because another driver is on the vehicle,
// starts no trip: the running trip's next fix at the loop's terminal is still
// at its last stop, not sent back to its first.
func TestHandleStartTrip_RefusedStartKeepsTheRunningTripsPlace(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t3 := mustTrip(t, ix, "T3")
	tracker := newPlacingTracker(t, ix)

	require.Equal(t, 4, reportOn(t, tracker, "T3", "20260902", t3.Shape.PointAt(t3.Shape.Length-30)).StopSequence)

	start := handleStartTrip(&mockTripStarter{err: ErrVehicleBusy}, nil, tracker)
	require.Equal(t, http.StatusConflict, tripRequest(t, start, "43", StartTripRequest{VehicleID: "bus-1"}).Code)

	assert.Equal(t, 4, reportOn(t, tracker, "T3", "20260902", loopClosingCorner(t3)).StopSequence)
}

// tripsOnBus1 is a store whose driver starts and ends every trip on bus-1.
type tripsOnBus1 struct{ noopStore }

func (*tripsOnBus1) StartTrip(_ context.Context, userID int64, vehicleID, routeID, gtfsTripID string) (*TripResponse, error) {
	return &TripResponse{ID: 2, UserID: userID, VehicleID: vehicleID, RouteID: routeID, GtfsTripID: gtfsTripID, Status: "active"}, nil
}

func (*tripsOnBus1) EndTrip(_ context.Context, _, _ int64) (string, error) {
	return "bus-1", nil
}

// TestTripRoutes_NewTripStartsAtItsFirstStop drives the next run of a loop
// through the real mux: the start route must mark the vehicle on the same
// tracker the location route places its reports with.
func TestTripRoutes_NewTripStartsAtItsFirstStop(t *testing.T) {
	ix := locatorFixtureIndex(t)
	t3 := mustTrip(t, ix, "T3")
	tracker := newPlacingTracker(t, ix)
	limiter := NewVehicleRateLimiter()
	t.Cleanup(limiter.Stop)
	mux := newMux(&tripsOnBus1{}, tracker, limiter, testSecret, testTTLs, time.Time{}, nil, nil, false, false, nil, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), nil, newFeedHealth(time.Now))
	token, err := generateJWT(&User{ID: 42, Email: "driver@test.com", Role: "driver"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	post := func(path string, body any) *httptest.ResponseRecorder {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	reportOn(t, tracker, "T3", "20260902", t3.Shape.PointAt(t3.Shape.Length-30))
	require.Equal(t, 4, reportOn(t, tracker, "T3", "20260902", loopClosingCorner(t3)).StopSequence)

	rec := post("/api/v1/trips/end", EndTripRequest{TripID: 1})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = post("/api/v1/trips/start", StartTripRequest{VehicleID: "bus-1", RouteID: "R2", GtfsTripID: "T5"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	pos := loopOpeningCorner(t3)
	rec = post("/api/v1/locations", map[string]any{
		"vehicle_id": "bus-1", "trip_id": "T5", "start_date": "20260902",
		"latitude": pos.Lat, "longitude": pos.Lon, "timestamp": time.Now().Unix(),
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	vehicles := tracker.ActiveVehicles()
	require.Len(t, vehicles, 1)
	assert.Equal(t, 1, vehicles[0].StopSequence, "the next run starts at its first stop")
}
