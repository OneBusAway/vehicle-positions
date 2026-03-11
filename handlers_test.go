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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

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
			Bearing:   180,
			Speed:     8.5,
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
}

func TestGetFeed_Protobuf(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100})

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
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100})

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
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100})
	time.Sleep(5 * time.Millisecond)

	handler := handleGetFeed(tracker)
	w := getFeed(handler, "")

	var feed gtfs.FeedMessage
	err := proto.Unmarshal(w.Body.Bytes(), &feed)
	require.NoError(t, err)
	assert.Empty(t, feed.Entity, "stale vehicle should be excluded from feed")
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
			loc:  LocationReport{Latitude: 1, Longitude: 2, Timestamp: 100},
			want: "vehicle_id is required",
		},
		{
			name: "latitude too high",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 91, Longitude: 2, Timestamp: 100},
			want: "latitude must be between -90 and 90",
		},
		{
			name: "latitude too low",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: -91, Longitude: 2, Timestamp: 100},
			want: "latitude must be between -90 and 90",
		},
		{
			name: "longitude too high",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 181, Timestamp: 100},
			want: "longitude must be between -180 and 180",
		},
		{
			name: "reject null coordinates (0,0)",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 0, Longitude: 0, Timestamp: 100},
			want: "latitude and longitude cannot both be zero (likely GPS error)",
		},
		{
			name: "zero timestamp",
			loc:  LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 0},
			want: "timestamp must be positive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create handler with nil store — validation happens first
			handler := handlePostLocation(nil, tracker)
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

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100})
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: 200})

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

type mockStore struct {
	err   error
	saved bool
}

func (m *mockStore) SaveLocation(ctx context.Context, loc *LocationReport) error {
	if m.err != nil {
		return m.err
	}
	m.saved = true
	return nil
}

func TestHandlePostLocation_HappyPath(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker)

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100}
	w := postLocation(handler, loc)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved, "location should be saved to store")
	assert.Len(t, tracker.ActiveVehicles(), 1, "tracker should be updated")
}

func TestHandlePostLocation_StoreFailure(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{err: fmt.Errorf("database down")}
	handler := handlePostLocation(mStore, tracker)

	loc := LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100}
	w := postLocation(handler, loc)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Len(t, tracker.ActiveVehicles(), 0, "tracker should NOT be updated on DB failure")
}

func TestHandlePostLocation_InvalidJSON(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker)

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
	handler := handlePostLocation(nil, tracker)
	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100}`)

	w := postLocationWithContentType(handler, body, "")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)

	w = postLocationWithContentType(handler, body, "text/plain")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestHandlePostLocation_ContentTypeWithCharsetAccepted(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	mStore := &mockStore{}
	handler := handlePostLocation(mStore, tracker)
	body := []byte(`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":100}`)

	w := postLocationWithContentType(handler, body, "application/json; charset=utf-8")
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved, "location should be saved for valid application/json content type")
func TestHandlePostLocation_UnknownFieldRejected(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	handler := handlePostLocation(nil, tracker)

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
	handler := handlePostLocation(nil, tracker)

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
	handler := handlePostLocation(nil, tracker)

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
	handler := handlePostLocation(nil, tracker)

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
	handler := handlePostLocation(mStore, tracker)

	body := []byte("{\"vehicle_id\":\"bus-1\",\"latitude\":1,\"longitude\":2,\"timestamp\":100}   \n")
	w := postLocationWithBody(handler, body, "application/json")

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, mStore.saved, "location should be saved when only trailing whitespace exists")
}
