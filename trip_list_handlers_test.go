package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTripLister implements TripLister for tests.
type mockTripLister struct {
	trips  []TripResponse
	err    error
	called bool
	// Capture args for verification.
	gotStatus    *string
	gotVehicleID *string
	gotUserID    *int64
	gotLimit     int32
	gotOffset    int32
}

func (m *mockTripLister) ListTrips(ctx context.Context, status *string, vehicleID *string, userID *int64, limit, offset int32) ([]TripResponse, error) {
	m.called = true
	m.gotStatus = status
	m.gotVehicleID = vehicleID
	m.gotUserID = userID
	m.gotLimit = limit
	m.gotOffset = offset
	if m.err != nil {
		return nil, m.err
	}
	return m.trips, nil
}

// mockTripGetter implements TripGetter for tests.
type mockTripGetter struct {
	trip *TripResponse
	err  error
}

func (m *mockTripGetter) GetTrip(ctx context.Context, id int64) (*TripResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.trip, nil
}

func listTripsRequest(t *testing.T, queryString string) *http.Request {
	t.Helper()
	return httptest.NewRequest("GET", "/api/v1/admin/trips"+queryString, nil)
}

func TestHandleListTrips_Success(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	endTime := now.Add(2 * time.Hour)
	mock := &mockTripLister{
		trips: []TripResponse{
			{
				ID: 1, UserID: 5, VehicleID: "bus-42", RouteID: "route-5",
				GtfsTripID: "route_5_0830", StartTime: now, EndTime: &endTime,
				Status: "completed", CreatedAt: now, UpdatedAt: now,
			},
		},
	}

	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, ""))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	var trips []TripResponse
	require.NoError(t, json.Unmarshal(resp["trips"], &trips))
	require.Len(t, trips, 1)
	assert.Equal(t, int64(1), trips[0].ID)
	assert.Equal(t, "bus-42", trips[0].VehicleID)

	var count int
	require.NoError(t, json.Unmarshal(resp["count"], &count))
	assert.Equal(t, 1, count)
}

func TestHandleListTrips_FilterByStatus(t *testing.T) {
	mock := &mockTripLister{trips: make([]TripResponse, 0)}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, "?status=active"))

	assert.Equal(t, http.StatusOK, w.Code)
	require.True(t, mock.called)
	require.NotNil(t, mock.gotStatus)
	assert.Equal(t, "active", *mock.gotStatus)
}

func TestHandleListTrips_FilterByVehicle(t *testing.T) {
	mock := &mockTripLister{trips: make([]TripResponse, 0)}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, "?vehicle_id=bus-42"))

	assert.Equal(t, http.StatusOK, w.Code)
	require.True(t, mock.called)
	require.NotNil(t, mock.gotVehicleID)
	assert.Equal(t, "bus-42", *mock.gotVehicleID)
}

func TestHandleListTrips_FilterByUserID(t *testing.T) {
	mock := &mockTripLister{trips: make([]TripResponse, 0)}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, "?user_id=5"))

	assert.Equal(t, http.StatusOK, w.Code)
	require.True(t, mock.called)
	require.NotNil(t, mock.gotUserID)
	assert.Equal(t, int64(5), *mock.gotUserID)
}

func TestHandleListTrips_FilterByMultiple(t *testing.T) {
	mock := &mockTripLister{trips: make([]TripResponse, 0)}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, "?status=active&vehicle_id=bus-1&user_id=3"))

	assert.Equal(t, http.StatusOK, w.Code)
	require.True(t, mock.called)
	require.NotNil(t, mock.gotStatus)
	assert.Equal(t, "active", *mock.gotStatus)
	require.NotNil(t, mock.gotVehicleID)
	assert.Equal(t, "bus-1", *mock.gotVehicleID)
	require.NotNil(t, mock.gotUserID)
	assert.Equal(t, int64(3), *mock.gotUserID)
}

func TestHandleListTrips_DefaultParams(t *testing.T) {
	mock := &mockTripLister{trips: make([]TripResponse, 0)}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, ""))

	assert.Equal(t, http.StatusOK, w.Code)
	require.True(t, mock.called)
	assert.Nil(t, mock.gotStatus)
	assert.Nil(t, mock.gotVehicleID)
	assert.Nil(t, mock.gotUserID)
	assert.Equal(t, int32(50), mock.gotLimit)
	assert.Equal(t, int32(0), mock.gotOffset)
}

func TestHandleListTrips_InvalidStatus(t *testing.T) {
	mock := &mockTripLister{}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, "?status=foo"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "status must be 'active' or 'completed'")
	assert.False(t, mock.called)
}

func TestHandleListTrips_InvalidLimit(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"zero", "?limit=0"},
		{"negative", "?limit=-1"},
		{"non-numeric", "?limit=abc"},
		{"too large", "?limit=1001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTripLister{}
			handler := handleListTrips(mock)
			w := httptest.NewRecorder()
			handler(w, listTripsRequest(t, tt.query))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodeError(t, w), "limit must be between 1 and 1000")
			assert.False(t, mock.called)
		})
	}
}

func TestHandleListTrips_InvalidOffset(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"negative", "?offset=-1"},
		{"non-numeric", "?offset=abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTripLister{}
			handler := handleListTrips(mock)
			w := httptest.NewRecorder()
			handler(w, listTripsRequest(t, tt.query))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodeError(t, w), "offset must be a non-negative integer")
			assert.False(t, mock.called)
		})
	}
}

func TestHandleListTrips_InvalidUserID(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"non-numeric", "?user_id=abc"},
		{"negative", "?user_id=-1"},
		{"zero", "?user_id=0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTripLister{}
			handler := handleListTrips(mock)
			w := httptest.NewRecorder()
			handler(w, listTripsRequest(t, tt.query))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodeError(t, w), "user_id must be a positive integer")
			assert.False(t, mock.called)
		})
	}
}

func TestHandleListTrips_EmptyResult(t *testing.T) {
	mock := &mockTripLister{trips: make([]TripResponse, 0)}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, ""))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Trips []TripResponse `json:"trips"`
		Count int            `json:"count"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Trips, "trips must be empty array, not null")
	assert.Len(t, resp.Trips, 0)
	assert.Equal(t, 0, resp.Count)

	// Also verify raw JSON contains [] not null.
	w2 := httptest.NewRecorder()
	handler(w2, listTripsRequest(t, ""))
	assert.Contains(t, w2.Body.String(), `"trips":[]`)
}

func TestHandleListTrips_StoreError(t *testing.T) {
	mock := &mockTripLister{err: errors.New("db connection lost")}
	handler := handleListTrips(mock)
	w := httptest.NewRecorder()
	handler(w, listTripsRequest(t, ""))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, decodeError(t, w), "failed to list trips")
}

func TestHandleListTrips_BoundaryLimit(t *testing.T) {
	t.Run("1000 accepted", func(t *testing.T) {
		mock := &mockTripLister{trips: make([]TripResponse, 0)}
		handler := handleListTrips(mock)
		w := httptest.NewRecorder()
		handler(w, listTripsRequest(t, "?limit=1000"))

		assert.Equal(t, http.StatusOK, w.Code)
		require.True(t, mock.called)
		assert.Equal(t, int32(1000), mock.gotLimit)
	})

	t.Run("1001 rejected", func(t *testing.T) {
		mock := &mockTripLister{}
		handler := handleListTrips(mock)
		w := httptest.NewRecorder()
		handler(w, listTripsRequest(t, "?limit=1001"))

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decodeError(t, w), "limit must be between 1 and 1000")
		assert.False(t, mock.called)
	})
}

func TestHandleGetTrip_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	endTime := now.Add(2 * time.Hour)
	mock := &mockTripGetter{
		trip: &TripResponse{
			ID: 1, UserID: 5, VehicleID: "bus-42", RouteID: "route-5",
			GtfsTripID: "route_5_0830", StartTime: now, EndTime: &endTime,
			Status: "completed", CreatedAt: now, UpdatedAt: now,
		},
	}

	handler := handleGetTrip(mock)
	req := httptest.NewRequest("GET", "/api/v1/admin/trips/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var trip TripResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&trip))
	assert.Equal(t, int64(1), trip.ID)
	assert.Equal(t, "bus-42", trip.VehicleID)
	assert.Equal(t, "completed", trip.Status)
	require.NotNil(t, trip.EndTime)
	assert.Equal(t, endTime, *trip.EndTime)
	assert.Equal(t, now, trip.CreatedAt)
	assert.Equal(t, now, trip.UpdatedAt)
}

func TestHandleGetTrip_NotFound(t *testing.T) {
	mock := &mockTripGetter{err: ErrTripNotFound}
	handler := handleGetTrip(mock)
	req := httptest.NewRequest("GET", "/api/v1/admin/trips/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, decodeError(t, w), "trip not found")
}

func TestHandleGetTrip_InvalidID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"non-numeric", "abc"},
		{"negative", "-1"},
		{"zero", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTripGetter{}
			handler := handleGetTrip(mock)
			req := httptest.NewRequest("GET", "/api/v1/admin/trips/"+tt.id, nil)
			req.SetPathValue("id", tt.id)
			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodeError(t, w), "invalid trip id")
		})
	}
}

func TestHandleGetTrip_StoreError(t *testing.T) {
	mock := &mockTripGetter{err: errors.New("db connection lost")}
	handler := handleGetTrip(mock)
	req := httptest.NewRequest("GET", "/api/v1/admin/trips/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, decodeError(t, w), "failed to get trip")
}

func TestHandleGetTrip_NullableEndTime_Nil(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mock := &mockTripGetter{
		trip: &TripResponse{
			ID: 1, UserID: 5, VehicleID: "bus-42", Status: "active",
			StartTime: now, CreatedAt: now, UpdatedAt: now,
		},
	}

	handler := handleGetTrip(mock)
	req := httptest.NewRequest("GET", "/api/v1/admin/trips/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify end_time is omitted from JSON.
	body := w.Body.String()
	assert.NotContains(t, body, "end_time", "end_time should be omitted for active trips")
}

func TestHandleGetTrip_NullableEndTime_Present(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	endTime := now.Add(2 * time.Hour)
	mock := &mockTripGetter{
		trip: &TripResponse{
			ID: 1, UserID: 5, VehicleID: "bus-42", Status: "completed",
			StartTime: now, EndTime: &endTime, CreatedAt: now, UpdatedAt: now,
		},
	}

	handler := handleGetTrip(mock)
	req := httptest.NewRequest("GET", "/api/v1/admin/trips/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var trip TripResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&trip))
	require.NotNil(t, trip.EndTime, "end_time should be present for completed trips")
	assert.Equal(t, endTime, *trip.EndTime)
}
