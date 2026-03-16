package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func float64ptr(v float64) *float64 {
	return &v
}

func postLocation(handler http.HandlerFunc, loc LocationReport) *httptest.ResponseRecorder {
	body, _ := json.Marshal(loc)
	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func postLocationWithBody(handler http.HandlerFunc, body []byte, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func getFeed(handler http.HandlerFunc, query string) *httptest.ResponseRecorder {
	url := "/gtfs-rt/vehicle-positions"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestBuildFeed_Empty(t *testing.T) {
	feed := buildFeed(nil)

	require.NotNil(t, feed.Header)
	assert.Equal(t, "2.0", feed.Header.GetGtfsRealtimeVersion())
	assert.Equal(t, gtfs.FeedHeader_FULL_DATASET, feed.Header.GetIncrementality())
	assert.NotZero(t, feed.Header.GetTimestamp())
	assert.Empty(t, feed.Entity)
}

func TestBuildFeed_WithVehicles(t *testing.T) {
	vehicles := []*VehicleState{
		{
			VehicleID: "bus-1",
			TripID:    "route-5",
			Latitude:  -1.29,
			Longitude: 36.82,
			Bearing:   float64ptr(180),
			Speed:     float64ptr(8.5),
			Timestamp: 1752566400,
		},
		{
			VehicleID: "bus-2",
			Latitude:  -1.30,
			Longitude: 36.83,
			Timestamp: 1752566500,
		},
	}

	feed := buildFeed(vehicles)

	require.Len(t, feed.Entity, 2)

	// Find bus-1
	var bus1, bus2 *gtfs.FeedEntity
	for _, e := range feed.Entity {
		switch e.GetId() {
		case "bus-1":
			bus1 = e
		case "bus-2":
			bus2 = e
		}
	}

	require.NotNil(t, bus1)
	assert.Equal(t, float32(-1.29), bus1.Vehicle.Position.GetLatitude())
	assert.Equal(t, float32(36.82), bus1.Vehicle.Position.GetLongitude())
	assert.Equal(t, float32(180), bus1.Vehicle.Position.GetBearing())
	assert.Equal(t, float32(8.5), bus1.Vehicle.Position.GetSpeed())
	assert.Equal(t, uint64(1752566400), bus1.Vehicle.GetTimestamp())
	assert.Equal(t, "route-5", bus1.Vehicle.Trip.GetTripId())

	require.NotNil(t, bus2)
	assert.Nil(t, bus2.Vehicle.Trip, "bus-2 has no trip, Trip should be nil")

	// E012: header timestamp must be >= max entity timestamp.
	// 1752566500 is the larger of the two vehicle timestamps above (bus-2).
	assert.GreaterOrEqual(t, feed.Header.GetTimestamp(), uint64(1752566500),
		"header timestamp must be >= max entity timestamp (E012)")
}

func TestGetFeed_Protobuf(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})

	handler := handleGetFeed(tracker)
	w := getFeed(handler, "")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-protobuf", w.Header().Get("Content-Type"))

	var feed gtfs.FeedMessage
	err := proto.Unmarshal(w.Body.Bytes(), &feed)
	require.NoError(t, err)
	require.Len(t, feed.Entity, 1)
	assert.Equal(t, "bus-1", feed.Entity[0].GetId())
}

func TestGetFeed_JSON(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})

	handler := handleGetFeed(tracker)
	w := getFeed(handler, "format=json")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var feed gtfs.FeedMessage
	err := protojson.Unmarshal(w.Body.Bytes(), &feed)
	require.NoError(t, err)
	require.Len(t, feed.Entity, 1)
}

func TestGetFeed_Empty(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)

	handler := handleGetFeed(tracker)
	w := getFeed(handler, "")

	assert.Equal(t, http.StatusOK, w.Code)

	var feed gtfs.FeedMessage
	err := proto.Unmarshal(w.Body.Bytes(), &feed)
	require.NoError(t, err)
	assert.Empty(t, feed.Entity)
}

func TestGetFeed_StaleExcluded(t *testing.T) {
	tracker := NewTracker(1 * time.Millisecond)
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})

	handler := handleGetFeed(tracker)
	assert.Eventually(t, func() bool {
		w := getFeed(handler, "")
		var feed gtfs.FeedMessage
		if err := proto.Unmarshal(w.Body.Bytes(), &feed); err != nil {
			return false
		}
		return len(feed.Entity) == 0
	}, 100*time.Millisecond, 2*time.Millisecond, "stale vehicle should be excluded from feed")
}

func TestHandlePostLocation_Validation(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	// Use a nil store — validation happens before DB call
	// We need a real Store for the handler signature, but validation errors
	// are returned before SaveLocation is called, so it won't be used.

	tests := []struct {
		name string
		loc  LocationReport
		want string
	}{
		{
			name: "missing vehicle_id",
			loc:  LocationReport{Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()},
			want: "vehicle_id is required",
		},
		{
			name: "latitude too high",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 91, Longitude: 2, Timestamp: time.Now().Unix()},
			want: "latitude must be between -90 and 90",
		},
		{
			name: "latitude too low",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: -91, Longitude: 2, Timestamp: time.Now().Unix()},
			want: "latitude must be between -90 and 90",
		},
		{
			name: "longitude too high",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 181, Timestamp: time.Now().Unix()},
			want: "longitude must be between -180 and 180",
		},
		{
			name: "reject null coordinates (0,0)",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 0, Longitude: 0, Timestamp: time.Now().Unix()},
			want: "latitude and longitude cannot both be zero (likely GPS error)",
		},
		{
			name: "zero timestamp",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 0},
			want: "timestamp must be positive",
		},
		{
			name: "vehicle_id too long",
			loc:  LocationReport{VehicleID: "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()},
			want: "vehicle_id must be at most 50 characters",
		},
		{
			name: "vehicle_id with spaces",
			loc:  LocationReport{VehicleID: "bus 1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()},
			want: "vehicle_id must contain only alphanumeric characters, dots, hyphens, and underscores",
		},
		{
			name: "vehicle_id with special characters",
			loc:  LocationReport{VehicleID: "bus@1!", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()},
			want: "vehicle_id must contain only alphanumeric characters, dots, hyphens, and underscores",
		},
		{
			name: "timestamp too far in past",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100},
			want: "timestamp must be within 5 minutes of server time",
		},
		{
			name: "timestamp too far in future",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix() + 600},
			want: "timestamp must be within 5 minutes of server time",
		},
		{
			name: "bearing negative",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Bearing: float64ptr(-1), Timestamp: time.Now().Unix()},
			want: "bearing must be between 0 and 360 (inclusive)",
		},
		{
			name: "bearing too high",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Bearing: float64ptr(361), Timestamp: time.Now().Unix()},
			want: "bearing must be between 0 and 360 (inclusive)",
		},
		{
			name: "speed negative",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Speed: float64ptr(-5), Timestamp: time.Now().Unix()},
			want: "speed must be non-negative",
		},
		{
			name: "bearing just below zero",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Bearing: float64ptr(-0.001), Timestamp: time.Now().Unix()},
			want: "bearing must be between 0 and 360 (inclusive)",
		},
		{
			name: "bearing just above 360",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Bearing: float64ptr(360.001), Timestamp: time.Now().Unix()},
			want: "bearing must be between 0 and 360 (inclusive)",
		},
		{
			name: "speed just below zero",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Speed: float64ptr(-0.001), Timestamp: time.Now().Unix()},
			want: "speed must be non-negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create handler with nil store — validation happens first
			handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())
			w := postLocation(handler, tc.loc)

			assert.Equal(t, http.StatusBadRequest, w.Code)

			var resp map[string]string
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Contains(t, resp["error"], tc.want)
		})
	}
}

func TestHandleAdminStatus_Empty(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	handler := handleAdminStatus(tracker, time.Now())
	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp adminStatusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, 0, resp.ActiveVehicles)
	assert.Equal(t, 0, resp.TotalVehiclesTracked)
	assert.Nil(t, resp.LastUpdate)
}

func TestHandleAdminStatus_WithVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()})
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: time.Now().Unix()})

	handler := handleAdminStatus(tracker, time.Now())
	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp adminStatusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, 2, resp.ActiveVehicles)
	assert.Equal(t, 2, resp.TotalVehiclesTracked)
	require.NotNil(t, resp.LastUpdate)
	assert.False(t, resp.LastUpdate.IsZero())
}

func TestHandleAdminStatus_Uptime(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	startTime := time.Now().Add(-10 * time.Second)
	handler := handleAdminStatus(tracker, startTime)
	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	var resp adminStatusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, resp.UptimeSeconds, int64(10))
}

func TestHandleAdminStatus_ZeroVehiclesNotNull(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	handler := handleAdminStatus(tracker, time.Now())
	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	var raw map[string]any
	err := json.NewDecoder(w.Body).Decode(&raw)
	require.NoError(t, err)

	assert.Equal(t, float64(0), raw["active_vehicles"])
	assert.Equal(t, float64(0), raw["total_vehicles_tracked"])
}

func TestHandlePostLocation_ValidVehicleIDBoundary(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	// Exactly 50 chars with hyphens and underscores
	loc := LocationReport{VehicleID: "abcdefghij-klmnopqrst_uvwxyz-1234567890_abcdefghij", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	require.Len(t, loc.VehicleID, 50)
	w := postLocationWithClaims(handler, loc, jwt.MapClaims{"sub": "driver-1"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved)
}

type mockStore struct {
	err          error
	saved        bool
	saveCount    int
	lastDriverID string
}

type mockHealthChecker struct {
	err error
}

func (m mockHealthChecker) Ping(ctx context.Context) error {
	return m.err
}

func (m *mockStore) SaveLocation(ctx context.Context, loc *LocationReport) error {
	if m.err != nil {
		return m.err
	}
	m.saved = true
	m.saveCount++
	m.lastDriverID = loc.DriverID
	return nil
}

func postLocationWithClaims(handler http.HandlerFunc, loc LocationReport, claims jwt.MapClaims) *httptest.ResponseRecorder {
	body, _ := json.Marshal(loc)
	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	w := httptest.NewRecorder()
	handler(w, req.WithContext(ctx))
	return w
}

func TestHandlePostLocation_HappyPath(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	w := postLocationWithClaims(handler, loc, jwt.MapClaims{"sub": "driver-1"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved, "location should be saved to store")
	assert.Len(t, tracker.ActiveVehicles(), 1, "tracker should be updated")
}

func TestHandleReadiness_OK(t *testing.T) {
	handler := handleReadiness(mockHealthChecker{})
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp readinessResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
}

func TestHandleReadiness_DBUnavailable(t *testing.T) {
	handler := handleReadiness(mockHealthChecker{err: fmt.Errorf("db down")})
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp readinessResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "degraded", resp.Status)
}

func TestHandlePostLocation_StoreFailure(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{err: fmt.Errorf("database down")}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	w := postLocationWithClaims(handler, loc, jwt.MapClaims{"sub": "driver-1"})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Len(t, tracker.ActiveVehicles(), 0, "tracker should NOT be updated on DB failure")
}

func TestHandlePostLocation_InvalidJSON(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())

	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "invalid JSON")
}

func TestHandlePostLocation_ContentTypeRequired(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())
	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100}`)

	w := postLocationWithBody(handler, body, "")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "Content-Type must be application/json")

	w = postLocationWithBody(handler, body, "text/plain")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "Content-Type must be application/json")
}

func TestHandlePostLocation_ContentTypeWithCharsetAccepted(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	body := []byte(fmt.Sprintf(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":%d}`, time.Now().Unix()))
	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	ctx := context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": "driver-1"})
	w := httptest.NewRecorder()
	handler(w, req.WithContext(ctx))

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved, "location should be saved for valid application/json content type")
}

func TestHandlePostLocation_UnknownFieldRejected(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())

	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100,"extra":"x"}`)
	w := postLocationWithBody(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "unknown field")
}

func TestHandlePostLocation_TrailingJSONRejected(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())

	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100}{"x":1}`)
	w := postLocationWithBody(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "single JSON object")
}

func TestHandlePostLocation_TrailingEmptyJSONObjectRejected(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())

	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100}{}`)
	w := postLocationWithBody(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "single JSON object")
}

func TestHandlePostLocation_TrailingGarbageRejected(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker, NewVehicleRateLimiter())

	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100}GARBAGE`)
	w := postLocationWithBody(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "invalid JSON:")
}

func TestHandlePostLocation_TrailingWhitespaceAccepted(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	body := []byte(fmt.Sprintf("{\"vehicle_id\":\"bus-1\",\"latitude\":1,\"longitude\":2,\"timestamp\":%d}   \n", time.Now().Unix()))
	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": "driver-1"})
	w := httptest.NewRecorder()
	handler(w, req.WithContext(ctx))

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved, "location should be saved when only trailing whitespace exists")
}

func TestHandlePostLocation_DriverIDExtractedFromClaims(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	claims := jwt.MapClaims{"sub": "42", "email": "driver@test.com", "role": "driver"}

	w := postLocationWithClaims(handler, loc, claims)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "42", mStore.lastDriverID, "driver_id must be populated from JWT sub claim")
}

// TestHandlePostLocation_MissingJWTContext replaces the old
// TestHandlePostLocation_DriverIDEmptyWithoutJWTContext.
// Missing claims now means requireAuth middleware did not run — a server
// misconfiguration — so the handler must return 500 and must not save.
func TestHandlePostLocation_MissingJWTContext(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	w := postLocation(handler, loc)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "missing JWT context must return 500, not silently proceed")
	assert.False(t, mStore.saved, "location must not be saved when JWT context is absent")

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "internal server error", resp["error"])
}

// TestHandlePostLocation_MissingSubClaim verifies that a JWT with no "sub"
// returns 401 rather than proceeding with an empty driver_id.
func TestHandlePostLocation_MissingSubClaim(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	w := postLocationWithClaims(handler, loc, jwt.MapClaims{"email": "driver@test.com"})

	assert.Equal(t, http.StatusUnauthorized, w.Code, "missing sub claim must return 401")
	assert.False(t, mStore.saved, "location must not be saved when sub claim is absent")

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "missing subject")
}

// TestHandlePostLocation_EmptySubClaim verifies that a JWT with sub == ""
// returns 401 rather than saving a location with an empty driver_id.
func TestHandlePostLocation_EmptySubClaim(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	w := postLocationWithClaims(handler, loc, jwt.MapClaims{"sub": ""})

	assert.Equal(t, http.StatusUnauthorized, w.Code, "empty sub claim must return 401")
	assert.False(t, mStore.saved, "location must not be saved when sub claim is empty")
}

func TestHandlePostLocation_DriverIDNotAcceptedFromClient(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	body := []byte(fmt.Sprintf(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":%d,"driver_id":"spoofed"}`, time.Now().Unix()))
	w := postLocationWithBody(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code, "driver_id in JSON body must be rejected as unknown field")
}

// TestHandlePostLocation_RateLimitBlocksSecondRequest uses postLocationWithClaims
// because the rate limiter is now keyed on driver_id from JWT sub, not vehicle_id.
func TestHandlePostLocation_RateLimitBlocksSecondRequest(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	rl := NewVehicleRateLimiter()
	handler := handlePostLocation(mStore, tracker, rl)

	loc := LocationReport{VehicleID: "bus-rl", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	claims := jwt.MapClaims{"sub": "driver-rl"}

	w1 := postLocationWithClaims(handler, loc, claims)
	assert.Equal(t, http.StatusCreated, w1.Code, "first request must succeed")

	w2 := postLocationWithClaims(handler, loc, claims)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code, "immediate second request must be rate limited")

	var resp map[string]string
	err := json.NewDecoder(w2.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "rate limit exceeded")
}

// TestHandlePostLocation_RateLimitNotSavedOnExcess uses postLocationWithClaims
// because the rate limiter is now keyed on driver_id from JWT sub, not vehicle_id.
func TestHandlePostLocation_RateLimitNotSavedOnExcess(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	rl := NewVehicleRateLimiter()
	handler := handlePostLocation(mStore, tracker, rl)

	loc := LocationReport{VehicleID: "bus-flood", Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}
	claims := jwt.MapClaims{"sub": "driver-flood"}

	postLocationWithClaims(handler, loc, claims)
	mStore.saveCount = 0

	postLocationWithClaims(handler, loc, claims)
	assert.Equal(t, 0, mStore.saveCount, "rate-limited request must not reach the store")
}

// TestHandlePostLocation_RateLimitDifferentVehiclesAreIndependent uses
// postLocationWithClaims because rate limiting is now per driver_id, not vehicle_id.
func TestHandlePostLocation_RateLimitDifferentVehiclesAreIndependent(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
	mStore := &mockStore{}
	rl := NewVehicleRateLimiter()
	handler := handlePostLocation(mStore, tracker, rl)

	now := time.Now().Unix()
	locA := LocationReport{VehicleID: "bus-A", Latitude: 1, Longitude: 2, Timestamp: now}
	locB := LocationReport{VehicleID: "bus-B", Latitude: 1, Longitude: 2, Timestamp: now}
	claimsA := jwt.MapClaims{"sub": "driver-A"}
	claimsB := jwt.MapClaims{"sub": "driver-B"}

	assert.Equal(t, http.StatusCreated, postLocationWithClaims(handler, locA, claimsA).Code)
	assert.Equal(t, http.StatusCreated, postLocationWithClaims(handler, locB, claimsB).Code)

	assert.Equal(t, http.StatusTooManyRequests, postLocationWithClaims(handler, locA, claimsA).Code)
	assert.Equal(t, http.StatusTooManyRequests, postLocationWithClaims(handler, locB, claimsB).Code)
}

func TestHandlePostLocation_ExplicitZeroOptionalFieldsPreserved(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	body := []byte(fmt.Sprintf(`{
		"vehicle_id":"bus-1",
		"latitude":1,
		"longitude":2,
		"bearing":0,
		"speed":0,
		"accuracy":0,
		"timestamp":%d
	}`, time.Now().Unix()))

	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": "driver-zero"})
	w := httptest.NewRecorder()

	handler(w, req.WithContext(ctx))

	assert.Equal(t, http.StatusCreated, w.Code)

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)

	require.NotNil(t, active[0].Bearing)
	assert.Equal(t, 0.0, *active[0].Bearing)

	require.NotNil(t, active[0].Speed)
	assert.Equal(t, 0.0, *active[0].Speed)

	require.NotNil(t, active[0].Accuracy)
	assert.Equal(t, 0.0, *active[0].Accuracy)
}

func TestHandlePostLocation_MissingOptionalFieldsRemainNil(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())

	body := []byte(fmt.Sprintf(`{
		"vehicle_id":"bus-1",
		"latitude":1,
		"longitude":2,
		"timestamp":%d
	}`, time.Now().Unix()))

	req := httptest.NewRequest("POST", "/api/v1/locations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), claimsKey, jwt.MapClaims{"sub": "driver-nil"})
	w := httptest.NewRecorder()

	handler(w, req.WithContext(ctx))

	assert.Equal(t, http.StatusCreated, w.Code)

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Nil(t, active[0].Bearing)
	assert.Nil(t, active[0].Speed)
	assert.Nil(t, active[0].Accuracy)
}

func TestBuildFeed_PreservesExplicitZeroBearingAndSpeed(t *testing.T) {
	zero := 0.0
	vehicles := []*VehicleState{
		{
			VehicleID: "bus-1",
			Latitude:  1,
			Longitude: 2,
			Bearing:   &zero,
			Speed:     &zero,
			Timestamp: time.Now().Unix(),
		},
	}

	feed := buildFeed(vehicles)
	require.Len(t, feed.Entity, 1)

	pos := feed.Entity[0].Vehicle.Position
	require.NotNil(t, pos.Bearing)
	assert.Equal(t, float32(0), pos.GetBearing())

	require.NotNil(t, pos.Speed)
	assert.Equal(t, float32(0), pos.GetSpeed())
}

func TestBuildFeed_OmitsUnsetBearingAndSpeed(t *testing.T) {
	vehicles := []*VehicleState{
		{
			VehicleID: "bus-1",
			Latitude:  1,
			Longitude: 2,
			Timestamp: time.Now().Unix(),
		},
	}

	feed := buildFeed(vehicles)
	require.Len(t, feed.Entity, 1)

	pos := feed.Entity[0].Vehicle.Position
	assert.Equal(t, float32(1), pos.GetLatitude())
	assert.Equal(t, float32(2), pos.GetLongitude())
	assert.Nil(t, pos.Bearing)
	assert.Nil(t, pos.Speed)
}

func TestHandlePostLocation_BearingSpeedBoundaries(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tests := []struct {
		name string
		sub  string
		loc  LocationReport
	}{
		{"bearing exactly 0 (north)", "driver-bearing-0", LocationReport{
			VehicleID: "bus-1", Latitude: 1, Longitude: 2,
			Bearing: float64ptr(0), Timestamp: time.Now().Unix()}},
		{"bearing exactly 360", "driver-bearing-360", LocationReport{
			VehicleID: "bus-2", Latitude: 1, Longitude: 2,
			Bearing: float64ptr(360), Timestamp: time.Now().Unix()}},
		{"bearing 359.99", "driver-bearing-359", LocationReport{
			VehicleID: "bus-3", Latitude: 1, Longitude: 2,
			Bearing: float64ptr(359.99), Timestamp: time.Now().Unix()}},
		{"speed exactly 0 (stationary)", "driver-speed-0", LocationReport{
			VehicleID: "bus-4", Latitude: 1, Longitude: 2,
			Speed: float64ptr(0), Timestamp: time.Now().Unix()}},
		{"nil bearing (not provided)", "driver-bearing-nil", LocationReport{
			VehicleID: "bus-5", Latitude: 1, Longitude: 2,
			Timestamp: time.Now().Unix()}},
		{"nil speed (not provided)", "driver-speed-nil", LocationReport{
			VehicleID: "bus-6", Latitude: 1, Longitude: 2,
			Timestamp: time.Now().Unix()}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mStore := &mockStore{}
			handler := handlePostLocation(mStore, tracker, NewVehicleRateLimiter())
			w := postLocationWithClaims(handler, tc.loc, jwt.MapClaims{"sub": tc.sub})
			assert.Equal(t, http.StatusCreated, w.Code, "boundary value should be accepted")
			assert.True(t, mStore.saved, "boundary value should have been saved")
		})
	}
}
