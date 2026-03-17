package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLocationHistoryLister struct {
	points []LocationPoint
	err    error
}

func (m *mockLocationHistoryLister) GetLocationHistory(_ context.Context, _ string, _, _ int64, _ int) ([]LocationPoint, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.points, nil
}

type mockVehicleChecker struct {
	exists bool
	err    error
}

func (m *mockVehicleChecker) VehicleExists(_ context.Context, _ string) (bool, error) {
	return m.exists, m.err
}

func newHistoryRequest(vehicleID string, query string) *http.Request {
	path := "/api/v1/admin/vehicles/placeholder/locations"
	if query != "" {
		path += "?" + query
	}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.SetPathValue("vehicleID", vehicleID)
	return r
}

func float64Ptr(v float64) *float64 {
	return &v
}

func TestHandleGetLocationHistory_Success(t *testing.T) {
	now := time.Now().UTC()
	lister := &mockLocationHistoryLister{
		points: []LocationPoint{
			{
				Latitude: -1.29, Longitude: 36.82,
				Bearing: float64Ptr(180.0), Speed: float64Ptr(8.5), Accuracy: float64Ptr(12.0),
				Timestamp: now.Unix(), TripID: "trip-1", ReceivedAt: now,
			},
		},
	}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("vehicle-042", ""))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp locationHistoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "vehicle-042", resp.VehicleID)
	assert.Equal(t, 1, resp.Count)
	require.Len(t, resp.Locations, 1)
	assert.Equal(t, -1.29, resp.Locations[0].Latitude)
	assert.Equal(t, 36.82, resp.Locations[0].Longitude)
	require.NotNil(t, resp.Locations[0].Bearing)
	assert.Equal(t, 180.0, *resp.Locations[0].Bearing)
	require.NotNil(t, resp.Locations[0].Speed)
	assert.Equal(t, 8.5, *resp.Locations[0].Speed)
	assert.Equal(t, "trip-1", resp.Locations[0].TripID)
}

func TestHandleGetLocationHistory_CSV(t *testing.T) {
	now := time.Now().UTC()
	lister := &mockLocationHistoryLister{
		points: []LocationPoint{
			{
				Latitude: -1.29, Longitude: 36.82,
				Bearing: float64Ptr(180.0), Speed: float64Ptr(8.5), Accuracy: float64Ptr(12.0),
				Timestamp: 1752566400, TripID: "trip-1", ReceivedAt: now,
			},
		},
	}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("vehicle-042", "format=csv"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/csv", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "vehicle-042_locations.csv")

	reader := csv.NewReader(w.Body)
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2, "header + 1 data row")

	assert.Equal(t, []string{"timestamp", "latitude", "longitude", "bearing", "speed", "accuracy", "trip_id", "recorded_at"}, records[0])
	assert.Equal(t, "1752566400", records[1][0])
	assert.Equal(t, "-1.29", records[1][1])
	assert.Equal(t, "36.82", records[1][2])
	assert.Equal(t, "180", records[1][3])
	assert.Equal(t, "8.5", records[1][4])
	assert.Equal(t, "12", records[1][5])
	assert.Equal(t, "trip-1", records[1][6])
}

func TestHandleGetLocationHistory_DefaultParams(t *testing.T) {
	lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", ""))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp locationHistoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Count)
	assert.NotNil(t, resp.Locations, "locations should be empty array, not null")
	assert.Len(t, resp.Locations, 0)
}

func TestHandleGetLocationHistory_EmptyHistory(t *testing.T) {
	lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-empty", ""))

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify JSON contains [] not null
	body := w.Body.String()
	assert.Contains(t, body, `"locations":[]`)
}

func TestHandleGetLocationHistory_VehicleNotFound(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{exists: false}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("ghost-bus", ""))

	assert.Equal(t, http.StatusNotFound, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "vehicle not found")
}

func TestHandleGetLocationHistory_InvalidVehicleID(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{}
	handler := handleGetLocationHistory(lister, checker)

	tests := []struct {
		name      string
		vehicleID string
		wantErr   string
	}{
		{"special characters", "bus@#$", "alphanumeric"},
		{"spaces", "bus 1", "alphanumeric"},
		{"empty", "", "vehicle_id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, newHistoryRequest(tt.vehicleID, ""))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Contains(t, resp["error"], tt.wantErr)
		})
	}
}

func TestHandleGetLocationHistory_VehicleIDTooLong(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{}
	handler := handleGetLocationHistory(lister, checker)

	// Exactly 50 chars should pass validation (will hit vehicle check)
	w50 := httptest.NewRecorder()
	checker.exists = true
	lister.points = make([]LocationPoint, 0)
	handler.ServeHTTP(w50, newHistoryRequest(strings.Repeat("a", 50), ""))
	assert.Equal(t, http.StatusOK, w50.Code, "50-char vehicle_id should be accepted")

	// 51 chars should be rejected
	w51 := httptest.NewRecorder()
	handler.ServeHTTP(w51, newHistoryRequest(strings.Repeat("a", 51), ""))
	assert.Equal(t, http.StatusBadRequest, w51.Code, "51-char vehicle_id should be rejected")

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w51.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "at most 50")
}

func TestHandleGetLocationHistory_InvalidLimit(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	tests := []struct {
		name  string
		query string
	}{
		{"zero", "limit=0"},
		{"negative", "limit=-1"},
		{"over max", "limit=1001"},
		{"non-numeric", "limit=abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, newHistoryRequest("bus-1", tt.query))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Contains(t, resp["error"], "limit")
		})
	}
}

func TestHandleGetLocationHistory_InvalidTimestamp(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	tests := []struct {
		name    string
		query   string
		wantErr string
	}{
		{"non-numeric from", "from=abc", "from"},
		{"non-numeric to", "to=xyz", "to"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, newHistoryRequest("bus-1", tt.query))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Contains(t, resp["error"], tt.wantErr)
		})
	}
}

func TestHandleGetLocationHistory_FromGreaterThanTo(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", "from=2000&to=1000"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "from must be less than or equal to to")
}

func TestHandleGetLocationHistory_StoreError(t *testing.T) {
	lister := &mockLocationHistoryLister{err: fmt.Errorf("database connection lost")}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", ""))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "internal server error")
}

func TestHandleGetLocationHistory_CheckerError(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{err: fmt.Errorf("database connection lost")}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", ""))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "internal server error")
}

func TestHandleGetLocationHistory_NullableFieldsJSON(t *testing.T) {
	lister := &mockLocationHistoryLister{
		points: []LocationPoint{
			{Latitude: 1.0, Longitude: 2.0, Bearing: nil, Speed: nil, Accuracy: nil, Timestamp: time.Now().Unix(), ReceivedAt: time.Now()},
			{Latitude: 3.0, Longitude: 4.0, Bearing: float64Ptr(0), Speed: float64Ptr(0), Accuracy: float64Ptr(0), Timestamp: time.Now().Unix() - 60, ReceivedAt: time.Now()},
		},
	}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", ""))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp locationHistoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Locations, 2)

	// First: nil fields
	assert.Nil(t, resp.Locations[0].Bearing, "nil bearing should serialize as null")
	assert.Nil(t, resp.Locations[0].Speed)
	assert.Nil(t, resp.Locations[0].Accuracy)

	// Second: zero-valued fields (not nil)
	require.NotNil(t, resp.Locations[1].Bearing, "zero bearing should not be nil")
	assert.Equal(t, 0.0, *resp.Locations[1].Bearing)
	require.NotNil(t, resp.Locations[1].Speed)
	assert.Equal(t, 0.0, *resp.Locations[1].Speed)
}

func TestHandleGetLocationHistory_CSVNullableFields(t *testing.T) {
	lister := &mockLocationHistoryLister{
		points: []LocationPoint{
			{Latitude: 1.0, Longitude: 2.0, Bearing: nil, Speed: nil, Accuracy: nil, Timestamp: 1752566400, ReceivedAt: time.Now()},
		},
	}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", "format=csv"))

	reader := csv.NewReader(w.Body)
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)

	// bearing, speed, accuracy columns should be empty for nil
	assert.Equal(t, "", records[1][3], "nil bearing should be empty in CSV")
	assert.Equal(t, "", records[1][4], "nil speed should be empty in CSV")
	assert.Equal(t, "", records[1][5], "nil accuracy should be empty in CSV")
}

func TestHandleGetLocationHistory_BoundaryLimit(t *testing.T) {
	lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	// limit=1 should be accepted
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newHistoryRequest("bus-1", "limit=1"))
	assert.Equal(t, http.StatusOK, w1.Code, "limit=1 should be accepted")

	// limit=1000 should be accepted
	w1000 := httptest.NewRecorder()
	handler.ServeHTTP(w1000, newHistoryRequest("bus-1", "limit=1000"))
	assert.Equal(t, http.StatusOK, w1000.Code, "limit=1000 should be accepted")

	// limit=1001 should be rejected
	w1001 := httptest.NewRecorder()
	handler.ServeHTTP(w1001, newHistoryRequest("bus-1", "limit=1001"))
	assert.Equal(t, http.StatusBadRequest, w1001.Code, "limit=1001 should be rejected")
}

func TestHandleGetLocationHistory_InvalidFormat(t *testing.T) {
	lister := &mockLocationHistoryLister{}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", "format=xml"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "format must be json or csv")
}
