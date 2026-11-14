package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHistorySaver struct {
	err       error
	userID    int64
	vehicleID string
	saved     []*LocationReport
}

func (f *fakeHistorySaver) SaveLocationHistory(_ context.Context, userID int64, vehicleID string, points []*LocationReport) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.userID, f.vehicleID, f.saved = userID, vehicleID, points
	return len(points), nil
}

func postHistory(t *testing.T, store LocationHistorySaver, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations/history", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": "42"}))
	w := httptest.NewRecorder()
	handlePostLocationHistory(store, NewKeyedRateLimiter(historyUploadInterval, historyUploadBurst, false))(w, req)
	return w
}

func historyPoint(age time.Duration) HistoryPoint {
	return HistoryPoint{TripID: "T1", RouteID: "R1", Latitude: 47.6, Longitude: -122.33, Timestamp: time.Now().Add(-age).Unix()}
}

// TestHandlePostLocationHistory_StoresOldFixes: a fix from an hour ago, which
// the realtime endpoint refuses, is exactly what this endpoint is for.
func TestHandlePostLocationHistory_StoresOldFixes(t *testing.T) {
	store := &fakeHistorySaver{}
	w := postHistory(t, store, LocationHistoryRequest{VehicleID: "bus-1", Points: []HistoryPoint{historyPoint(time.Hour), historyPoint(59 * time.Minute)}})

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"saved":2,"invalid":0}`, w.Body.String())
	assert.Equal(t, int64(42), store.userID)
	assert.Equal(t, "bus-1", store.vehicleID)
	require.Len(t, store.saved, 2)
	assert.Equal(t, "42", store.saved[0].DriverID)
	assert.Equal(t, "bus-1", store.saved[0].VehicleID)

	loc := LocationReport{VehicleID: "bus-1", Latitude: 47.6, Longitude: -122.33, Timestamp: historyPoint(time.Hour).Timestamp}
	assert.Error(t, loc.validate(), "the realtime endpoint still refuses the same fix")
}

func TestHandlePostLocationHistory_Rejects(t *testing.T) {
	tooMany := make([]HistoryPoint, maxHistoryBatch+1)
	for i := range tooMany {
		tooMany[i] = historyPoint(time.Minute)
	}
	tests := []struct {
		name    string
		body    LocationHistoryRequest
		message string
	}{
		{"empty batch", LocationHistoryRequest{VehicleID: "bus-1"}, "points must hold 1 to 500 fixes"},
		{"oversized batch", LocationHistoryRequest{VehicleID: "bus-1", Points: tooMany}, "points must hold 1 to 500 fixes"},
		{"no vehicle", LocationHistoryRequest{Points: []HistoryPoint{historyPoint(time.Minute)}}, "vehicle id is required"},
		{"bad vehicle", LocationHistoryRequest{VehicleID: "bus 1", Points: []HistoryPoint{historyPoint(time.Minute)}}, "vehicle id must contain only"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeHistorySaver{}
			w := postHistory(t, store, tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tc.message)
			assert.Nil(t, store.saved)
		})
	}
}

// TestHandlePostLocationHistory_SkipsInvalidPoints: one bad point does not cost
// the rest of the batch, which the phone deletes once it gets 201.
func TestHandlePostLocationHistory_SkipsInvalidPoints(t *testing.T) {
	store := &fakeHistorySaver{}
	tooOld := historyPoint(25 * time.Hour)
	noFix := historyPoint(time.Minute)
	noFix.Latitude, noFix.Longitude = 0, 0
	w := postHistory(t, store, LocationHistoryRequest{VehicleID: "bus-1", Points: []HistoryPoint{tooOld, historyPoint(time.Hour), noFix}})

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"saved":1,"invalid":2}`, w.Body.String())
	require.Len(t, store.saved, 1)
	assert.Equal(t, historyPoint(time.Hour).Timestamp, store.saved[0].Timestamp)

	store = &fakeHistorySaver{}
	w = postHistory(t, store, LocationHistoryRequest{VehicleID: "bus-1", Points: []HistoryPoint{tooOld}})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"saved":0,"invalid":1}`, w.Body.String())
	assert.Nil(t, store.saved, "nothing to store")
}

// TestHandlePostLocationHistory_NullReadings: a phone sends null for a reading
// it did not have, which is stored as unknown, while a real zero (due north,
// standing still) stays a zero.
func TestHandlePostLocationHistory_NullReadings(t *testing.T) {
	ts := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	body := json.RawMessage(`{"vehicle_id":"bus-1","points":[
		{"latitude":47.6,"longitude":-122.33,"bearing":null,"speed":null,"accuracy":null,"timestamp":` + ts + `},
		{"latitude":47.6,"longitude":-122.33,"bearing":0,"speed":0,"accuracy":0,"timestamp":` + ts + `}]}`)
	store := &fakeHistorySaver{}
	w := postHistory(t, store, body)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"saved":2,"invalid":0}`, w.Body.String())
	require.Len(t, store.saved, 2)
	assert.Nil(t, store.saved[0].Bearing)
	assert.Nil(t, store.saved[0].Speed)
	assert.Nil(t, store.saved[0].Accuracy)
	for _, v := range []*float64{store.saved[1].Bearing, store.saved[1].Speed, store.saved[1].Accuracy} {
		require.NotNil(t, v)
		assert.Zero(t, *v)
	}
}

func TestHandlePostLocationHistory_NotAssigned(t *testing.T) {
	w := postHistory(t, &fakeHistorySaver{err: ErrNotAssigned}, LocationHistoryRequest{VehicleID: "bus-9", Points: []HistoryPoint{historyPoint(time.Minute)}})
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "not assigned"))
}

// TestStore_SaveLocationHistory checks the assignment, stores the batch once
// however often it is retried, and pins the queries a late upload would
// otherwise confuse: a trip's trail shows its backlog where it was driven,
// whenever it is uploaded, and no other trip's trail does; startup seeding does
// not take the backlog for the vehicle's latest position.
func TestStore_SaveLocationHistory(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	userID := setupTripUser(t, store, "Offline Driver", "offline@test.com", "bus-off", "Bus Off")
	trip, err := store.StartTrip(ctx, userID, "bus-off", "R1", "T1")
	require.NoError(t, err)
	driverID := strconv.FormatInt(userID, 10)
	// The trip began before the backlog's timestamps.
	now := time.Now()
	_, err = store.pool.Exec(ctx, "UPDATE trips SET start_time = $1 WHERE id = $2", now.Add(-15*time.Minute), trip.ID)
	require.NoError(t, err)
	latitudes := func(tripID int64) []float64 {
		t.Helper()
		pts, err := store.ListTripLocations(ctx, tripID)
		require.NoError(t, err)
		out := make([]float64, len(pts))
		for i, p := range pts {
			out[i] = p.Latitude
		}
		return out
	}
	point := func(lat float64, ts time.Time) *LocationReport {
		return &LocationReport{VehicleID: "bus-off", DriverID: driverID, Latitude: lat, Longitude: lat, Timestamp: ts.Unix()}
	}

	// A live report now, then the backlog from ten minutes ago arrives after it.
	require.NoError(t, store.SaveLocation(ctx, point(2, now)))
	backlog := []*LocationReport{point(1, now.Add(-10*time.Minute)), point(1.5, now.Add(-9*time.Minute))}
	_, err = store.SaveLocationHistory(ctx, userID+999, "bus-off", backlog)
	assert.ErrorIs(t, err, ErrNotAssigned)
	saved, err := store.SaveLocationHistory(ctx, userID, "bus-off", backlog)
	require.NoError(t, err)
	assert.Equal(t, 2, saved)
	assert.Equal(t, []float64{1, 1.5, 2}, latitudes(trip.ID), "in the order driven")

	// The phone never got the reply and sends the batch again.
	saved, err = store.SaveLocationHistory(ctx, userID, "bus-off", backlog)
	require.NoError(t, err)
	assert.Equal(t, 0, saved, "a retried upload stores nothing twice")
	assert.Equal(t, []float64{1, 1.5, 2}, latitudes(trip.ID))

	recent, err := store.GetRecentLocations(ctx, now.Add(-5*time.Minute))
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, 2.0, recent[0].Latitude, "the live report, not the backlog received after it")

	// The trip ends. A live report received afterwards stays out of its
	// trail; a backlog recorded during it but uploaded afterwards goes in.
	_, err = store.pool.Exec(ctx, "UPDATE trips SET status = 'completed', end_time = NOW() WHERE id = $1", trip.ID)
	require.NoError(t, err)
	require.NoError(t, store.SaveLocation(ctx, point(3, now.Add(time.Second))))
	_, err = store.SaveLocationHistory(ctx, userID, "bus-off", []*LocationReport{point(1.2, now.Add(-8*time.Minute))})
	require.NoError(t, err)
	assert.Equal(t, []float64{1, 1.5, 1.2, 2}, latitudes(trip.ID))

	// The same driver starts the next trip on the vehicle, and only then does
	// the last of the first trip's backlog go up. It belongs to the first trip
	// alone, even though it was received during the second.
	next, err := store.StartTrip(ctx, userID, "bus-off", "R1", "T2")
	require.NoError(t, err)
	require.NoError(t, store.SaveLocation(ctx, point(4, now.Add(2*time.Second))))
	_, err = store.SaveLocationHistory(ctx, userID, "bus-off", []*LocationReport{point(1.3, now.Add(-7*time.Minute))})
	require.NoError(t, err)
	assert.Equal(t, []float64{1, 1.5, 1.2, 1.3, 2}, latitudes(trip.ID))
	assert.Equal(t, []float64{4}, latitudes(next.ID), "no backlog from the trip before")
}

// TestStore_ListTripLocations_LiveFixFromTripBefore: after a short layover, a
// fix recorded during the first trip and received two minutes into the next
// is young enough to count as live, but it was not driven on the next trip.
func TestStore_ListTripLocations_LiveFixFromTripBefore(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	userID := setupTripUser(t, store, "Layover Driver", "layover@test.com", "bus-lay", "Bus Lay")
	driverID := strconv.FormatInt(userID, 10)
	now := time.Now()

	first, err := store.StartTrip(ctx, userID, "bus-lay", "R1", "T1")
	require.NoError(t, err)
	_, err = store.EndTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "UPDATE trips SET start_time = $1, end_time = $2 WHERE id = $3",
		now.Add(-10*time.Minute), now.Add(-150*time.Second), first.ID)
	require.NoError(t, err)
	next, err := store.StartTrip(ctx, userID, "bus-lay", "R1", "T2")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "UPDATE trips SET start_time = $1 WHERE id = $2", now.Add(-2*time.Minute), next.ID)
	require.NoError(t, err)

	fromFirst := &LocationReport{VehicleID: "bus-lay", DriverID: driverID, Latitude: 1, Longitude: 1, Timestamp: now.Add(-3 * time.Minute).Unix()}
	require.NoError(t, store.SaveLocation(ctx, fromFirst))
	own := &LocationReport{VehicleID: "bus-lay", DriverID: driverID, Latitude: 2, Longitude: 2, Timestamp: now.Unix()}
	require.NoError(t, store.SaveLocation(ctx, own))

	pts, err := store.ListTripLocations(ctx, next.ID)
	require.NoError(t, err)
	require.Len(t, pts, 1, "only the next trip's own fix")
	assert.Equal(t, 2.0, pts[0].Latitude)

	pts, err = store.ListTripLocations(ctx, first.ID)
	require.NoError(t, err)
	require.Len(t, pts, 1, "the fix recorded during the first trip")
	assert.Equal(t, 1.0, pts[0].Latitude)
}

// TestStore_ListTripLocations_UploadJustAfterEnd: the driver ends the trip as
// the phone comes back into coverage, and the queued fixes go up through
// POST /api/v1/locations/history a minute later. They are young enough to
// count as live but were received after the trip ended. timestamp is whole
// seconds, so a fix stamped with the second the trip ended in may be from
// after the end and stays out, as does one plainly recorded after it.
func TestStore_ListTripLocations_UploadJustAfterEnd(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	userID := setupTripUser(t, store, "Gap Driver", "gap@test.com", "bus-gap", "Bus Gap")
	driverID := strconv.FormatInt(userID, 10)
	now := time.Now()
	end := now.Add(-time.Minute).Truncate(time.Second).Add(500 * time.Millisecond)

	trip, err := store.StartTrip(ctx, userID, "bus-gap", "R1", "T1")
	require.NoError(t, err)
	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "UPDATE trips SET start_time = $1, end_time = $2 WHERE id = $3",
		now.Add(-20*time.Minute), end, trip.ID)
	require.NoError(t, err)

	fix := func(lat float64, at int64) *LocationReport {
		return &LocationReport{VehicleID: "bus-gap", DriverID: driverID, Latitude: lat, Longitude: lat, Timestamp: at}
	}
	inDeadZone := fix(1, end.Add(-time.Minute).Unix())
	inEndSecond := fix(2, end.Unix())
	afterEnd := fix(3, end.Add(30*time.Second).Unix())
	saved, err := store.SaveLocationHistory(ctx, userID, "bus-gap", []*LocationReport{inDeadZone, inEndSecond, afterEnd})
	require.NoError(t, err)
	require.Equal(t, 3, saved)

	pts, err := store.ListTripLocations(ctx, trip.ID)
	require.NoError(t, err)
	require.Len(t, pts, 1, "only the fix recorded before the trip ended")
	assert.Equal(t, 1.0, pts[0].Latitude)
}

// TestStore_ListTripLocations_BacklogFixInFirstSecond: start_time carries
// microseconds but timestamp is whole seconds, so a backlog fix recorded in the
// trip's first second is stamped just before the start and must still count.
func TestStore_ListTripLocations_BacklogFixInFirstSecond(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	userID := setupTripUser(t, store, "First Second Driver", "firstsecond@test.com", "bus-fs", "Bus FS")
	driverID := strconv.FormatInt(userID, 10)
	start := time.Now().Add(-10 * time.Minute).Truncate(time.Second).Add(500 * time.Millisecond)

	trip, err := store.StartTrip(ctx, userID, "bus-fs", "R1", "T1")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "UPDATE trips SET start_time = $1 WHERE id = $2", start, trip.ID)
	require.NoError(t, err)

	firstSecond := &LocationReport{VehicleID: "bus-fs", DriverID: driverID, Latitude: 1, Longitude: 1, Timestamp: start.Unix()}
	saved, err := store.SaveLocationHistory(ctx, userID, "bus-fs", []*LocationReport{firstSecond})
	require.NoError(t, err)
	require.Equal(t, 1, saved)

	pts, err := store.ListTripLocations(ctx, trip.ID)
	require.NoError(t, err)
	require.Len(t, pts, 1, "the backlog fix from the trip's first second")
	assert.Equal(t, 1.0, pts[0].Latitude)
}
