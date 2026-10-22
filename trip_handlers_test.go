package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// mockTripStarter implements TripStarter for tests.
type mockTripStarter struct {
	trip *TripResponse
	err  error
}

func (m *mockTripStarter) StartTrip(ctx context.Context, userID int64, vehicleID, routeID, gtfsTripID string) (*TripResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.trip, nil
}

// mockTripEnder implements TripEnder for tests.
type mockTripEnder struct {
	err error
}

func (m *mockTripEnder) EndTrip(ctx context.Context, tripID, userID int64) error {
	return m.err
}

// tripRequest sends a JSON request with JWT claims to the handler.
func tripRequest(t *testing.T, handler http.HandlerFunc, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return tripRawRequest(t, handler, userID, data)
}

// tripRawRequest sends a raw byte body with JWT claims to the handler.
func tripRawRequest(t *testing.T, handler http.HandlerFunc, userID string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": userID}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// decodeError decodes the JSON error response body and returns the "error" field.
func decodeError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp["error"]
}

func TestHandleStartTrip_Success(t *testing.T) {
	store := &mockTripStarter{trip: &TripResponse{
		ID:        1,
		UserID:    42,
		VehicleID: "bus-1",
		RouteID:   "route-5",
		Status:    "active",
	}}

	handler := handleStartTrip(store, nil)
	w := tripRequest(t, handler, "42", StartTripRequest{
		VehicleID: "bus-1",
		RouteID:   "route-5",
	})

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp TripResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.ID)
	assert.Equal(t, "bus-1", resp.VehicleID)
	assert.Equal(t, "active", resp.Status)
}

func TestHandleStartTrip_NotAssigned(t *testing.T) {
	store := &mockTripStarter{err: ErrNotAssigned}

	handler := handleStartTrip(store, nil)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1"})

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, decodeError(t, w), "not assigned")
}

func TestHandleStartTrip_AlreadyActive(t *testing.T) {
	store := &mockTripStarter{err: ErrActiveTripExists}

	handler := handleStartTrip(store, nil)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1"})

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, decodeError(t, w), "already has an active trip")
}

func TestHandleStartTrip_Validation(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	tests := []struct {
		name        string
		body        any
		raw         bool
		code        int
		errContains string
	}{
		{"missing vehicle_id", StartTripRequest{VehicleID: ""}, false, http.StatusBadRequest, "vehicle_id is required"},
		{"invalid JSON", nil, true, http.StatusBadRequest, "invalid JSON"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if tc.raw {
				w = tripRawRequest(t, handler, "42", []byte("{bad"))
			} else {
				w = tripRequest(t, handler, "42", tc.body)
			}
			assert.Equal(t, tc.code, w.Code)
			assert.Contains(t, decodeError(t, w), tc.errContains)
		})
	}
}

func TestHandleStartTrip_MissingClaims(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	body, _ := json.Marshal(StartTripRequest{VehicleID: "bus-1"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, decodeError(t, w), "internal server error")
}

func TestHandleEndTrip_Success(t *testing.T) {
	store := &mockTripEnder{}

	handler := handleEndTrip(store)
	w := tripRequest(t, handler, "42", EndTripRequest{TripID: 1})

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "trip ended", resp["status"])
}

func TestHandleEndTrip_NotFound(t *testing.T) {
	store := &mockTripEnder{err: ErrTripNotFound}

	handler := handleEndTrip(store)
	w := tripRequest(t, handler, "42", EndTripRequest{TripID: 999})

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, decodeError(t, w), "active trip not found")
}

func TestHandleEndTrip_Validation(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	tests := []struct {
		name        string
		body        any
		raw         bool
		code        int
		errContains string
	}{
		{"missing trip_id", EndTripRequest{TripID: 0}, false, http.StatusBadRequest, "trip_id is required"},
		{"negative trip_id", EndTripRequest{TripID: -1}, false, http.StatusBadRequest, "trip_id is required"},
		{"invalid JSON", nil, true, http.StatusBadRequest, "invalid JSON"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if tc.raw {
				w = tripRawRequest(t, handler, "42", []byte("{bad"))
			} else {
				w = tripRequest(t, handler, "42", tc.body)
			}
			assert.Equal(t, tc.code, w.Code)
			assert.Contains(t, decodeError(t, w), tc.errContains)
		})
	}
}

func TestHandleEndTrip_MissingClaims(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	body, _ := json.Marshal(EndTripRequest{TripID: 1})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, decodeError(t, w), "internal server error")
}

func TestHandleStartTrip_TrailingJSON(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	w := tripRawRequest(t, handler, "42", []byte(`{"vehicle_id":"bus-1"}{"extra":true}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "trailing data")
}

func TestHandleEndTrip_TrailingJSON(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	w := tripRawRequest(t, handler, "42", []byte(`{"trip_id":1}{"extra":true}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "trailing data")
}

func TestHandleStartTrip_UnknownFields(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	w := tripRawRequest(t, handler, "42", []byte(`{"vehicle_id":"bus-1","unknown_field":"value"}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "unknown field")
}

func TestHandleStartTrip_InvalidSubClaim(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	w := tripRawRequest(t, handler, "not-a-number", []byte(`{"vehicle_id":"bus-1"}`))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, decodeError(t, w), "invalid subject")
}

func TestHandleEndTrip_InvalidSubClaim(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	w := tripRawRequest(t, handler, "not-a-number", []byte(`{"trip_id":1}`))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, decodeError(t, w), "invalid subject")
}

func TestHandleStartTrip_EmptySub(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	w := tripRawRequest(t, handler, "", []byte(`{"vehicle_id":"bus-1"}`))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, decodeError(t, w), "missing subject")
}

func TestHandleStartTrip_WrongContentType(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	body, _ := json.Marshal(StartTripRequest{VehicleID: "bus-1"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Contains(t, decodeError(t, w), "Content-Type must be application/json")
}

func TestHandleEndTrip_WrongContentType(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	body, _ := json.Marshal(EndTripRequest{TripID: 1})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Contains(t, decodeError(t, w), "Content-Type must be application/json")
}

func TestHandleEndTrip_EmptySub(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	w := tripRawRequest(t, handler, "", []byte(`{"trip_id":1}`))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, decodeError(t, w), "missing subject")
}

func TestHandleEndTrip_UnknownFields(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	w := tripRawRequest(t, handler, "42", []byte(`{"trip_id":1,"unknown_field":"value"}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "unknown field")
}

func TestHandleStartTrip_RequestBodyTooLarge(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)

	largeBody := `{"vehicle_id":"` + strings.Repeat("a", 2048) + `"}`
	w := tripRawRequest(t, handler, "42", []byte(largeBody))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Contains(t, decodeError(t, w), "request body too large")
}

func TestHandleEndTrip_RequestBodyTooLarge(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	largeBody := `{"trip_id":1,"padding":"` + strings.Repeat("x", 2048) + `"}`
	w := tripRawRequest(t, handler, "42", []byte(largeBody))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Contains(t, decodeError(t, w), "request body too large")
}

func TestHandleStartTrip_VehicleIDFormat(t *testing.T) {
	store := &mockTripStarter{trip: &TripResponse{
		ID:        1,
		VehicleID: strings.Repeat("a", 50),
		Status:    "active",
	}}
	handler := handleStartTrip(store, nil)

	tests := []struct {
		name        string
		vehicleID   string
		code        int
		errContains string
	}{
		{"exactly 50 chars", strings.Repeat("a", 50), http.StatusCreated, ""},
		{"too long", strings.Repeat("a", 51), http.StatusBadRequest, "at most 50 characters"},
		{"special chars", "bus@1!", http.StatusBadRequest, "alphanumeric"},
		{"spaces", "bus 1", http.StatusBadRequest, "alphanumeric"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: tc.vehicleID})
			assert.Equal(t, tc.code, w.Code)
			if tc.errContains != "" {
				assert.Contains(t, decodeError(t, w), tc.errContains)
			}
		})
	}
}

func TestHandleStartTrip_RouteIDTooLong(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1", RouteID: strings.Repeat("r", 101)})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "route_id must be at most 100 characters")
}

func TestHandleStartTrip_GtfsTripIDTooLong(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store, nil)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1", GtfsTripID: strings.Repeat("g", 101)})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeError(t, w), "gtfs_trip_id must be at most 100 characters")
}

func TestHandleStartTrip_InternalServerError(t *testing.T) {
	store := &mockTripStarter{err: assert.AnError}
	handler := handleStartTrip(store, nil)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1"})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, decodeError(t, w), "failed to start trip")
}

func TestHandleEndTrip_InternalServerError(t *testing.T) {
	store := &mockTripEnder{err: assert.AnError}
	handler := handleEndTrip(store)
	w := tripRequest(t, handler, "42", EndTripRequest{TripID: 1})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, decodeError(t, w), "failed to end trip")
}

// TestHandleStartTrip_ChecksIDsAgainstSchedule rejects ids the static feed
// does not have, and a route that is not the trip's, so the feed never
// publishes a vehicle consumers cannot match.
func TestHandleStartTrip_ChecksIDsAgainstSchedule(t *testing.T) {
	catalog := testCatalog(t) // R1 runs T1 and T2; R2 runs T3
	tests := []struct {
		name, routeID, tripID string
		status                int
		message               string
	}{
		{"trip and route agree", "R1", "T1", http.StatusCreated, ""},
		{"route only", "R2", "", http.StatusCreated, ""},
		{"trip only", "", "T3", http.StatusCreated, ""},
		{"unknown route", "NOPE", "", http.StatusBadRequest, `route_id "NOPE" is not in the GTFS schedule`},
		{"unknown trip", "R1", "FAKE", http.StatusBadRequest, `gtfs_trip_id "FAKE" is not in the GTFS schedule`},
		{"trip on another route", "R1", "T3", http.StatusBadRequest, `gtfs_trip_id "T3" runs on route_id "R2", not "R1"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockTripStarter{trip: &TripResponse{ID: 1, UserID: 42, VehicleID: "bus-1", Status: "active"}}
			w := tripRequest(t, handleStartTrip(store, catalog), "42",
				StartTripRequest{VehicleID: "bus-1", RouteID: tc.routeID, GtfsTripID: tc.tripID})
			assert.Equal(t, tc.status, w.Code)
			if tc.message != "" {
				assert.Equal(t, tc.message, decodeError(t, w))
			}
		})
	}
}

// fixtureWithTripRenamed returns the fixture feed with trip_id "from" renamed
// to "to", as an agency's next feed version often regenerates its trip ids.
func fixtureWithTripRenamed(t *testing.T, from, to string) *rider.Index {
	t.Helper()
	zr, err := zip.OpenReader("rider/testdata/fixture.zip")
	require.NoError(t, err)
	defer zr.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		r, err := f.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(r)
		r.Close()
		require.NoError(t, err)
		if f.Name == "trips.txt" || f.Name == "stop_times.txt" {
			lines := strings.Split(string(body), "\n")
			for i, line := range lines {
				fields := strings.Split(line, ",")
				for j := range fields {
					if fields[j] == from {
						fields[j] = to
					}
				}
				lines[i] = strings.Join(fields, ",")
			}
			body = []byte(strings.Join(lines, "\n"))
		}
		w, err := zw.Create(f.Name)
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	ix, err := rider.IndexFromBytes(buf.Bytes(), "renamed", catalogNow)
	require.NoError(t, err)
	return ix
}

// TestScheduleSwapMidTrip_ReportsStillAccepted: the schedule check is made
// once, at trip start. A refresh that renames the trip while a driver is on it
// must not get their reports refused; the vehicle stays in the feed with the
// ids it started with until the trip ends.
func TestScheduleSwapMidTrip_ReportsStillAccepted(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })

	userID := setupTripUser(t, store, "Swap Driver", "swap@test.com", "bus-swap", "Bus Swap")
	sub := strconv.FormatInt(userID, 10)
	var current atomic.Pointer[rider.Index]
	current.Store(testCatalog(t).index())
	catalog := newGTFSCatalog(current.Load, rider.DefaultThresholds())
	catalog.now = func() time.Time { return catalogNow }

	w := tripRequest(t, handleStartTrip(store, catalog), sub, StartTripRequest{VehicleID: "bus-swap", RouteID: "R1", GtfsTripID: "T1"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	current.Store(fixtureWithTripRenamed(t, "T1", "T1-v2"))
	w = tripRequest(t, handleStartTrip(store, catalog), sub, StartTripRequest{VehicleID: "bus-swap", RouteID: "R1", GtfsTripID: "T1"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, `gtfs_trip_id "T1" is not in the GTFS schedule`, decodeError(t, w), "the new schedule no longer has T1")

	tracker := NewTracker(5 * time.Minute)
	report := LocationReport{VehicleID: "bus-swap", TripID: "T1", RouteID: "R1", Latitude: 47.6, Longitude: -122.33, Timestamp: time.Now().Unix()}
	w = postLocationWithClaims(handlePostLocation(store, tracker, NewVehicleRateLimiter()), report, jwt.MapClaims{"sub": sub})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	vehicles := tracker.ActiveVehicles()
	require.Len(t, vehicles, 1)
	assert.Equal(t, "T1", vehicles[0].TripID)
}
