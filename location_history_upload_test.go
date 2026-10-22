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
