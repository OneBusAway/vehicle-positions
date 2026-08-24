package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeVehicleLister struct{ vehicles []VehicleResponse }

func (f *fakeVehicleLister) ListVehicles(_ context.Context) ([]VehicleResponse, error) {
	return f.vehicles, nil
}
func (f *fakeVehicleLister) GetVehicle(_ context.Context, _ string) (*VehicleResponse, error) {
	panic("not implemented")
}
func (f *fakeVehicleLister) UpsertVehicle(_ context.Context, _, _, _ string) (*VehicleResponse, error) {
	panic("not implemented")
}
func (f *fakeVehicleLister) DeactivateVehicle(_ context.Context, _ string) error {
	panic("not implemented")
}

type fakeActiveTrips struct{ m map[string]ActiveTripInfo }

func (f *fakeActiveTrips) ListActiveTripsByVehicle(_ context.Context) (map[string]ActiveTripInfo, error) {
	return f.m, nil
}

type errVehicleLister struct{}

func (f *errVehicleLister) ListVehicles(_ context.Context) ([]VehicleResponse, error) {
	return nil, assert.AnError
}
func (f *errVehicleLister) GetVehicle(_ context.Context, _ string) (*VehicleResponse, error) {
	panic("not implemented")
}
func (f *errVehicleLister) UpsertVehicle(_ context.Context, _, _, _ string) (*VehicleResponse, error) {
	panic("not implemented")
}
func (f *errVehicleLister) DeactivateVehicle(_ context.Context, _ string) error {
	panic("not implemented")
}

type errActiveTrips struct{}

func (f *errActiveTrips) ListActiveTripsByVehicle(_ context.Context) (map[string]ActiveTripInfo, error) {
	return nil, assert.AnError
}

func TestHandleLiveVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	speed := 8.5
	tracker.Update(&LocationReport{VehicleID: "bus-1", TripID: "gtfs-77", Latitude: -1.29, Longitude: 36.82, Speed: &speed, Timestamp: 1752566400})
	tracker.Update(&LocationReport{VehicleID: "ghost-9", TripID: "", Latitude: 0.1, Longitude: 0.2, Timestamp: 1752566400})

	vehicles := &fakeVehicleLister{vehicles: []VehicleResponse{{ID: "bus-1", Label: "Bus One", Active: true}}}
	trips := &fakeActiveTrips{m: map[string]ActiveTripInfo{
		"bus-1": {TripID: 42, RouteID: "5", GtfsTripID: "gtfs-77", UserID: 3, DriverName: "Asha"},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, vehicles, trips).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count    int                `json:"count"`
		Vehicles []liveVehicleEntry `json:"vehicles"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 2, resp.Count)

	byID := map[string]liveVehicleEntry{}
	for _, v := range resp.Vehicles {
		byID[v.VehicleID] = v
	}
	b1 := byID["bus-1"]
	assert.Equal(t, "Bus One", b1.Label)
	require.NotNil(t, b1.TripDBID)
	assert.EqualValues(t, 42, *b1.TripDBID)
	assert.Equal(t, "Asha", *b1.DriverName)
	assert.Equal(t, "5", *b1.RouteID)
	assert.Nil(t, b1.Bearing)
	assert.Equal(t, 8.5, *b1.Speed)
	assert.EqualValues(t, 1752566400, b1.ReportedAt)

	g := byID["ghost-9"]
	assert.Equal(t, "ghost-9", g.Label, "label falls back to id when vehicle unknown")
	assert.Nil(t, g.TripDBID)
	assert.Nil(t, g.DriverName)
}

// TestHandleLiveVehicles_Sorted verifies vehicles are sorted by vehicle_id
// for stable output, regardless of tracker map iteration order.
func TestHandleLiveVehicles_Sorted(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	for _, id := range []string{"zebra", "alpha", "mike"} {
		tracker.Update(&LocationReport{VehicleID: id, Latitude: 1, Longitude: 1, Timestamp: 1752566400})
	}

	vehicles := &fakeVehicleLister{}
	trips := &fakeActiveTrips{m: map[string]ActiveTripInfo{}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, vehicles, trips).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count    int                `json:"count"`
		Vehicles []liveVehicleEntry `json:"vehicles"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Vehicles, 3)
	assert.Equal(t, []string{"alpha", "mike", "zebra"}, []string{
		resp.Vehicles[0].VehicleID, resp.Vehicles[1].VehicleID, resp.Vehicles[2].VehicleID,
	})
}

// TestHandleLiveVehicles_Empty verifies an empty tracker returns an empty
// (non-null) vehicles array and a zero count.
func TestHandleLiveVehicles_Empty(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	vehicles := &fakeVehicleLister{}
	trips := &fakeActiveTrips{m: map[string]ActiveTripInfo{}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, vehicles, trips).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count    int                `json:"count"`
		Vehicles []liveVehicleEntry `json:"vehicles"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Count)
	assert.NotNil(t, resp.Vehicles)
	assert.Len(t, resp.Vehicles, 0)
}

// TestHandleLiveVehicles_UpdatedAtFormat verifies UpdatedAt is RFC3339 UTC.
func TestHandleLiveVehicles_UpdatedAtFormat(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 1, Timestamp: 1752566400})

	vehicles := &fakeVehicleLister{}
	trips := &fakeActiveTrips{m: map[string]ActiveTripInfo{}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, vehicles, trips).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Vehicles []liveVehicleEntry `json:"vehicles"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Vehicles, 1)
	parsed, err := time.Parse(time.RFC3339, resp.Vehicles[0].UpdatedAt)
	require.NoError(t, err, "updated_at must be RFC3339")
	assert.Equal(t, time.UTC, parsed.Location())
}

// TestHandleLiveVehicles_VehicleStoreError verifies a ListVehicles error
// produces a 500 JSON error response.
func TestHandleLiveVehicles_VehicleStoreError(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 1, Timestamp: 1752566400})

	trips := &fakeActiveTrips{m: map[string]ActiveTripInfo{}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, &errVehicleLister{}, trips).ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["error"])
}

// TestHandleLiveVehicles_TripStoreError verifies a ListActiveTripsByVehicle
// error produces a 500 JSON error response.
func TestHandleLiveVehicles_TripStoreError(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 1, Timestamp: 1752566400})

	vehicles := &fakeVehicleLister{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vehicles/live", nil)
	w := httptest.NewRecorder()
	handleLiveVehicles(tracker, vehicles, &errActiveTrips{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["error"])
}
