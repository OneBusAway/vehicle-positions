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

// fakeTripLister is a TripLister test double that captures the TripFilter it
// was called with, so tests can assert query params were translated
// correctly.
type fakeTripLister struct {
	trips    []TripSummary
	err      error
	captured TripFilter
}

func (f *fakeTripLister) ListTrips(_ context.Context, filter TripFilter) ([]TripSummary, error) {
	f.captured = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.trips, nil
}

// fakeTripTrailStore is a TripTrailStore test double.
type fakeTripTrailStore struct {
	trip      *TripSummary
	tripErr   error
	points    []LocationPoint
	pointsErr error
}

func (f *fakeTripTrailStore) GetTripSummary(_ context.Context, _ int64) (*TripSummary, error) {
	return f.trip, f.tripErr
}

func (f *fakeTripTrailStore) ListTripLocations(_ context.Context, _ int64) ([]LocationPoint, error) {
	return f.points, f.pointsErr
}

// TestHandleListTrips_StatusFilterPassthrough verifies status, vehicle_id,
// and q query params are translated into the TripFilter passed to the store,
// with limit widened to limit+1 for hasMore detection.
func TestHandleListTrips_StatusFilterPassthrough(t *testing.T) {
	fake := &fakeTripLister{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?status=active&vehicle_id=bus-1&q=asha", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, TripFilter{Status: "active", VehicleID: "bus-1", Q: "asha", Limit: 51, Offset: 0}, fake.captured)
}

// TestHandleListTrips_HasMore verifies that when the store returns limit+1
// rows, the response reports has_more:true and trims to exactly limit rows.
func TestHandleListTrips_HasMore(t *testing.T) {
	trips := make([]TripSummary, 3)
	for i := range trips {
		trips[i] = TripSummary{ID: int64(i + 1)}
	}
	fake := &fakeTripLister{trips: trips}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?limit=2", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count   int           `json:"count"`
		HasMore bool          `json:"has_more"`
		Trips   []TripSummary `json:"trips"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.HasMore)
	assert.Equal(t, 2, resp.Count)
	require.Len(t, resp.Trips, 2)
	assert.Equal(t, 3, fake.captured.Limit, "store must be called with limit+1")
}

// TestHandleListTrips_NoMore verifies has_more is false when the store
// returns fewer rows than limit+1.
func TestHandleListTrips_NoMore(t *testing.T) {
	fake := &fakeTripLister{trips: []TripSummary{{ID: 1}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?limit=5", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		HasMore bool          `json:"has_more"`
		Trips   []TripSummary `json:"trips"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.HasMore)
	assert.Len(t, resp.Trips, 1)
}

// TestHandleListTrips_BadLimit verifies a non-numeric limit is rejected.
func TestHandleListTrips_BadLimit(t *testing.T) {
	fake := &fakeTripLister{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?limit=abc", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleListTrips_LimitOutOfRange verifies limit=0 and limit>200 are
// rejected.
func TestHandleListTrips_LimitOutOfRange(t *testing.T) {
	for _, limit := range []string{"0", "201", "-1"} {
		t.Run("limit="+limit, func(t *testing.T) {
			fake := &fakeTripLister{}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?limit="+limit, nil)
			w := httptest.NewRecorder()
			handleListTrips(fake).ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

// TestHandleListTrips_BadOffset verifies a non-numeric offset is rejected.
func TestHandleListTrips_BadOffset(t *testing.T) {
	fake := &fakeTripLister{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?offset=abc", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleListTrips_BadStatus verifies status values other than "",
// "active", or "completed" are rejected.
func TestHandleListTrips_BadStatus(t *testing.T) {
	fake := &fakeTripLister{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips?status=bogus", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleListTrips_Defaults verifies the default limit (50) and offset
// (0) are applied when the query params are omitted.
func TestHandleListTrips_Defaults(t *testing.T) {
	fake := &fakeTripLister{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 51, fake.captured.Limit)
	assert.Equal(t, 0, fake.captured.Offset)
}

// TestHandleListTrips_StoreError verifies a store error produces a 500 JSON
// error response.
func TestHandleListTrips_StoreError(t *testing.T) {
	fake := &fakeTripLister{err: assert.AnError}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips", nil)
	w := httptest.NewRecorder()
	handleListTrips(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestHandleTripLocations_NotFound verifies ErrTripNotFound from
// GetTripSummary produces a 404.
func TestHandleTripLocations_NotFound(t *testing.T) {
	fake := &fakeTripTrailStore{tripErr: ErrTripNotFound}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips/999/locations", nil)
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	handleTripLocations(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleTripLocations_NonNumericID verifies a non-numeric {id} path
// value produces a 404, same as an unknown numeric id.
func TestHandleTripLocations_NonNumericID(t *testing.T) {
	fake := &fakeTripTrailStore{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips/not-a-number/locations", nil)
	req.SetPathValue("id", "not-a-number")
	w := httptest.NewRecorder()
	handleTripLocations(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleTripLocations_HappyPath verifies the trip summary and point
// trail are mapped correctly, in particular LocationPoint.Timestamp ->
// reported_at (unix int) and ReceivedAt -> RFC3339 UTC string.
func TestHandleTripLocations_HappyPath(t *testing.T) {
	speed := 12.5
	bearing := 90.0
	trip := &TripSummary{ID: 5, VehicleID: "bus-1", Status: "completed"}
	points := []LocationPoint{
		{
			Latitude: -1.29, Longitude: 36.82,
			Bearing: &bearing, Speed: &speed,
			Timestamp:  1752566400,
			ReceivedAt: time.Unix(1752566405, 0),
		},
	}
	fake := &fakeTripTrailStore{trip: trip, points: points}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips/5/locations", nil)
	req.SetPathValue("id", "5")
	w := httptest.NewRecorder()
	handleTripLocations(fake).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Trip   TripSummary `json:"trip"`
		Points []struct {
			Latitude   float64  `json:"latitude"`
			Longitude  float64  `json:"longitude"`
			Bearing    *float64 `json:"bearing"`
			Speed      *float64 `json:"speed"`
			Accuracy   *float64 `json:"accuracy"`
			ReportedAt int64    `json:"reported_at"`
			ReceivedAt string   `json:"received_at"`
		} `json:"points"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, int64(5), resp.Trip.ID)
	assert.Equal(t, "bus-1", resp.Trip.VehicleID)
	require.Len(t, resp.Points, 1)
	p := resp.Points[0]
	assert.Equal(t, -1.29, p.Latitude)
	assert.Equal(t, 36.82, p.Longitude)
	require.NotNil(t, p.Bearing)
	assert.Equal(t, 90.0, *p.Bearing)
	require.NotNil(t, p.Speed)
	assert.Equal(t, 12.5, *p.Speed)
	assert.Nil(t, p.Accuracy)
	assert.EqualValues(t, 1752566400, p.ReportedAt)

	parsed, err := time.Parse(time.RFC3339, p.ReceivedAt)
	require.NoError(t, err, "received_at must be RFC3339")
	assert.Equal(t, time.UTC, parsed.Location())
	assert.Equal(t, int64(1752566405), parsed.Unix())
}

// TestHandleTripLocations_StoreError verifies a GetTripSummary error other
// than ErrTripNotFound produces a 500.
func TestHandleTripLocations_StoreError(t *testing.T) {
	fake := &fakeTripTrailStore{tripErr: assert.AnError}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips/5/locations", nil)
	req.SetPathValue("id", "5")
	w := httptest.NewRecorder()
	handleTripLocations(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestHandleTripLocations_LocationsStoreError verifies a ListTripLocations
// error produces a 500.
func TestHandleTripLocations_LocationsStoreError(t *testing.T) {
	fake := &fakeTripTrailStore{trip: &TripSummary{ID: 5}, pointsErr: assert.AnError}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/trips/5/locations", nil)
	req.SetPathValue("id", "5")
	w := httptest.NewRecorder()
	handleTripLocations(fake).ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
