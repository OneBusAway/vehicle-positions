package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func tripRequest(t *testing.T, handler http.HandlerFunc, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": userID}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleStartTrip_Success(t *testing.T) {
	store := &mockTripStarter{trip: &TripResponse{
		ID:        1,
		UserID:    42,
		VehicleID: "bus-1",
		RouteID:   "route-5",
		Status:    "active",
	}}

	handler := handleStartTrip(store)
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

	handler := handleStartTrip(store)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1"})

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleStartTrip_AlreadyActive(t *testing.T) {
	store := &mockTripStarter{err: ErrActiveTripExists}

	handler := handleStartTrip(store)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1"})

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandleStartTrip_Validation(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	tests := []struct {
		name string
		body any
		code int
	}{
		{"missing vehicle_id", StartTripRequest{VehicleID: ""}, http.StatusBadRequest},
		{"invalid JSON", "{bad", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if s, ok := tc.body.(string); ok {
				req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(s)))
				req.Header.Set("Content-Type", "application/json")
				claims := jwt.MapClaims{"sub": "42"}
				ctx := context.WithValue(req.Context(), claimsKey, claims)
				req = req.WithContext(ctx)
				w = httptest.NewRecorder()
				handler(w, req)
			} else {
				w = tripRequest(t, handler, "42", tc.body)
			}
			assert.Equal(t, tc.code, w.Code)
		})
	}
}

func TestHandleStartTrip_MissingClaims(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	body, _ := json.Marshal(StartTripRequest{VehicleID: "bus-1"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleEndTrip_Success(t *testing.T) {
	store := &mockTripEnder{}

	handler := handleEndTrip(store)
	w := tripRequest(t, handler, "42", EndTripRequest{TripID: 1})

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleEndTrip_NotFound(t *testing.T) {
	store := &mockTripEnder{err: ErrTripNotFound}

	handler := handleEndTrip(store)
	w := tripRequest(t, handler, "42", EndTripRequest{TripID: 999})

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleEndTrip_Validation(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	tests := []struct {
		name string
		body any
		code int
	}{
		{"missing trip_id", EndTripRequest{TripID: 0}, http.StatusBadRequest},
		{"negative trip_id", EndTripRequest{TripID: -1}, http.StatusBadRequest},
		{"invalid JSON", "{bad", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if s, ok := tc.body.(string); ok {
				req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(s)))
				req.Header.Set("Content-Type", "application/json")
				claims := jwt.MapClaims{"sub": "42"}
				ctx := context.WithValue(req.Context(), claimsKey, claims)
				req = req.WithContext(ctx)
				w = httptest.NewRecorder()
				handler(w, req)
			} else {
				w = tripRequest(t, handler, "42", tc.body)
			}
			assert.Equal(t, tc.code, w.Code)
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
}

func TestHandleStartTrip_TrailingJSON(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	body := []byte(`{"vehicle_id":"bus-1"}{"extra":true}`)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": "42"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleEndTrip_TrailingJSON(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	body := []byte(`{"trip_id":1}{"extra":true}`)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": "42"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleStartTrip_UnknownFields(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	body := []byte(`{"vehicle_id":"bus-1","unknown_field":"value"}`)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": "42"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleStartTrip_InvalidSubClaim(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	body, _ := json.Marshal(StartTripRequest{VehicleID: "bus-1"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// sub is not a valid integer string
	claims := jwt.MapClaims{"sub": "not-a-number"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleEndTrip_InvalidSubClaim(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	body, _ := json.Marshal(EndTripRequest{TripID: 1})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": "not-a-number"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleStartTrip_EmptySub(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	body, _ := json.Marshal(StartTripRequest{VehicleID: "bus-1"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	claims := jwt.MapClaims{"sub": ""}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleStartTrip_WrongContentType(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	body, _ := json.Marshal(StartTripRequest{VehicleID: "bus-1"})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	claims := jwt.MapClaims{"sub": "42"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestHandleEndTrip_WrongContentType(t *testing.T) {
	store := &mockTripEnder{}
	handler := handleEndTrip(store)

	body, _ := json.Marshal(EndTripRequest{TripID: 1})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	claims := jwt.MapClaims{"sub": "42"}
	ctx := context.WithValue(req.Context(), claimsKey, claims)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestHandleStartTrip_VehicleIDFormat(t *testing.T) {
	store := &mockTripStarter{}
	handler := handleStartTrip(store)

	tests := []struct {
		name      string
		vehicleID string
		code      int
	}{
		{"too long", strings.Repeat("a", 51), http.StatusBadRequest},
		{"special chars", "bus@1!", http.StatusBadRequest},
		{"spaces", "bus 1", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: tc.vehicleID})
			assert.Equal(t, tc.code, w.Code)
		})
	}
}

func TestHandleStartTrip_InternalServerError(t *testing.T) {
	store := &mockTripStarter{err: assert.AnError}
	handler := handleStartTrip(store)
	w := tripRequest(t, handler, "42", StartTripRequest{VehicleID: "bus-1"})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleEndTrip_InternalServerError(t *testing.T) {
	store := &mockTripEnder{err: assert.AnError}
	handler := handleEndTrip(store)
	w := tripRequest(t, handler, "42", EndTripRequest{TripID: 1})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
