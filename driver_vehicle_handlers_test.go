package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubDriverVehicleLister struct {
	vehicles []VehicleResponse
	err      error
	gotUser  int64
}

func (s *stubDriverVehicleLister) ListActiveVehiclesByUser(_ context.Context, userID int64) ([]VehicleResponse, error) {
	s.gotUser = userID
	return s.vehicles, s.err
}

func driverClaimsRequest(t *testing.T, sub string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	claims := jwt.MapClaims{"sub": sub, "role": "driver"}
	return req.WithContext(context.WithValue(req.Context(), claimsKey, claims))
}

func TestHandleListMyVehicles_ReturnsVehicles(t *testing.T) {
	stub := &stubDriverVehicleLister{vehicles: []VehicleResponse{{ID: "bus-1", Label: "Bus 1", Active: true}}}
	rec := httptest.NewRecorder()

	handleListMyVehicles(stub)(rec, driverClaimsRequest(t, "42"))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(42), stub.gotUser)
	var got []VehicleResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "bus-1", got[0].ID)
}

func TestHandleListMyVehicles_EmptyIsJSONArray(t *testing.T) {
	stub := &stubDriverVehicleLister{vehicles: nil}
	rec := httptest.NewRecorder()

	handleListMyVehicles(stub)(rec, driverClaimsRequest(t, "42"))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
}

func TestHandleListMyVehicles_StoreError(t *testing.T) {
	stub := &stubDriverVehicleLister{err: errors.New("boom")}
	rec := httptest.NewRecorder()

	handleListMyVehicles(stub)(rec, driverClaimsRequest(t, "42"))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleListMyVehicles_BadSubject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	claims := jwt.MapClaims{"sub": "not-a-number", "role": "driver"}
	req = req.WithContext(context.WithValue(req.Context(), claimsKey, claims))
	rec := httptest.NewRecorder()

	handleListMyVehicles(&stubDriverVehicleLister{})(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
