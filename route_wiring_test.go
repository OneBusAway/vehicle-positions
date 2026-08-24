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

// noopStore satisfies appStore with no-op method bodies.
// For driver-token tests, requireAdmin short-circuits before any store method
// is called. For admin-token tests, handlers do reach the store; stubs return
// zero values, which is safe for wiring-only assertions.
type noopStore struct{}

func (n *noopStore) GetUserByEmail(_ context.Context, _ string) (*User, error) {
	return nil, ErrUserNotFound
}
func (n *noopStore) ListUsers(_ context.Context) ([]UserResponse, error) {
	return make([]UserResponse, 0), nil
}
func (n *noopStore) GetUser(_ context.Context, _ int64) (*UserResponse, error) {
	return nil, nil
}
func (n *noopStore) CreateUser(_ context.Context, _, _, _, _ string) (*UserResponse, error) {
	return nil, nil
}
func (n *noopStore) UpdateUser(_ context.Context, _ int64, _, _, _ string) (*UserResponse, error) {
	return nil, nil
}
func (n *noopStore) DeleteUser(_ context.Context, _ int64) error {
	return nil
}
func (n *noopStore) ListVehicles(_ context.Context) ([]VehicleResponse, error) {
	return make([]VehicleResponse, 0), nil
}
func (n *noopStore) GetVehicle(_ context.Context, _ string) (*VehicleResponse, error) {
	return nil, nil
}
func (n *noopStore) UpsertVehicle(_ context.Context, _, _, _ string) (*VehicleResponse, error) {
	return nil, nil
}
func (n *noopStore) DeactivateVehicle(_ context.Context, _ string) error {
	return nil
}
func (n *noopStore) SaveLocation(_ context.Context, _ *LocationReport) error {
	return nil
}
func (n *noopStore) CreateAssignment(_ context.Context, _ int64, _ string) (*AssignmentResponse, error) {
	return nil, nil
}
func (n *noopStore) DeleteAssignment(_ context.Context, _ int64, _ string) error {
	return nil
}
func (n *noopStore) ListAssignmentsByUser(_ context.Context, _ int64) ([]AssignmentResponse, error) {
	return make([]AssignmentResponse, 0), nil
}
func (n *noopStore) ListAssignmentsByVehicle(_ context.Context, _ string) ([]AssignmentResponse, error) {
	return make([]AssignmentResponse, 0), nil
}
func (n *noopStore) StartTrip(_ context.Context, _ int64, _, _, _ string) (*TripResponse, error) {
	return nil, nil
}
func (n *noopStore) EndTrip(_ context.Context, _, _ int64) error {
	return nil
}
func (n *noopStore) Ping(_ context.Context) error {
	return nil
}
func (n *noopStore) GetLocationHistory(_ context.Context, _ string, _, _ int64, _ int) ([]LocationPoint, error) {
	return make([]LocationPoint, 0), nil
}
func (n *noopStore) VehicleExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (n *noopStore) ListActiveVehiclesByUser(_ context.Context, _ int64) ([]VehicleResponse, error) {
	return make([]VehicleResponse, 0), nil
}
func (n *noopStore) SetUserActive(_ context.Context, _ int64, _ bool) error {
	return nil
}
func (n *noopStore) CountUsersByRole(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (n *noopStore) CountActiveUsersByRole(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (n *noopStore) UpdateVehicleInfo(_ context.Context, _, _, _ string) error {
	return nil
}
func (n *noopStore) SetVehicleActive(_ context.Context, _ string, _ bool) error {
	return nil
}
func (n *noopStore) CountActiveVehicles(_ context.Context) (int, error) {
	return 0, nil
}
func (n *noopStore) CountActiveTrips(_ context.Context) (int, error) {
	return 0, nil
}
func (n *noopStore) ListTrips(_ context.Context, _ TripFilter) ([]TripSummary, error) {
	return nil, nil
}
func (n *noopStore) GetTripSummary(_ context.Context, _ int64) (*TripSummary, error) {
	return nil, nil
}
func (n *noopStore) ListTripLocations(_ context.Context, _ int64) ([]LocationPoint, error) {
	return nil, nil
}
func (n *noopStore) ListActiveTripsByVehicle(_ context.Context) (map[string]ActiveTripInfo, error) {
	return nil, nil
}

// TestAdminRoutes_DriverTokenRejected verifies that every /api/v1/admin/* route
// is wrapped with adminMiddleware. A valid driver-role JWT must receive 403 on
// all admin routes — not 200, 401, or 404 — proving the middleware is wired.
// Add new admin routes to the table so this test catches future wiring gaps.
func TestAdminRoutes_DriverTokenRejected(t *testing.T) {
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	// nil tracker and rateLimiter are safe: adminMiddleware rejects driver
	// tokens before any handler body runs, so neither is dereferenced.
	mux := newMux(&noopStore{}, nil, nil, testSecret, time.Time{}, nil, false)

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/admin/status"},
		{"GET", "/api/v1/admin/vehicles"},
		{"GET", "/api/v1/admin/vehicles/bus-1"},
		{"POST", "/api/v1/admin/vehicles"},
		{"DELETE", "/api/v1/admin/vehicles/bus-1"},
		{"GET", "/api/v1/admin/vehicles/bus-1/locations"},
		{"GET", "/api/v1/admin/users"},
		{"GET", "/api/v1/admin/users/1"},
		{"POST", "/api/v1/admin/users"},
		{"PUT", "/api/v1/admin/users/1"},
		{"DELETE", "/api/v1/admin/users/1"},
		{"POST", "/api/v1/admin/assignments"},
		{"DELETE", "/api/v1/admin/users/1/vehicles/bus-1"},
		{"GET", "/api/v1/admin/users/1/vehicles"},
		{"GET", "/api/v1/admin/vehicles/bus-1/users"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+driverToken)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code, "route %s %s must require admin role", tc.method, tc.path)

			var resp map[string]string
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, "admin access required", resp["error"])
		})
	}
}

// TestAdminRoutes_AdminTokenAllowed verifies that an admin-role JWT is not
// blocked by adminMiddleware. Handler errors (nil store) are expected and
// irrelevant — we only assert the middleware itself does not return 403.
func TestAdminRoutes_AdminTokenAllowed(t *testing.T) {
	adminToken, err := generateJWT(&User{ID: 2, Email: "admin@test.com", Role: "admin"}, testSecret)
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	mux := newMux(&noopStore{}, tracker, nil, testSecret, time.Time{}, nil, false)

	// Same routes as the driver-rejection table — every admin route must
	// let a valid admin token through both middleware layers.
	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/admin/status"},
		{"GET", "/api/v1/admin/vehicles"},
		{"GET", "/api/v1/admin/vehicles/bus-1"},
		{"POST", "/api/v1/admin/vehicles"},
		{"DELETE", "/api/v1/admin/vehicles/bus-1"},
		{"GET", "/api/v1/admin/vehicles/bus-1/locations"},
		{"GET", "/api/v1/admin/users"},
		{"GET", "/api/v1/admin/users/1"},
		{"POST", "/api/v1/admin/users"},
		{"PUT", "/api/v1/admin/users/1"},
		{"DELETE", "/api/v1/admin/users/1"},
		{"POST", "/api/v1/admin/assignments"},
		{"DELETE", "/api/v1/admin/users/1/vehicles/bus-1"},
		{"GET", "/api/v1/admin/users/1/vehicles"},
		{"GET", "/api/v1/admin/vehicles/bus-1/users"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			assert.NotEqual(t, http.StatusForbidden, w.Code, "admin token must not be blocked by adminMiddleware on %s %s", tc.method, tc.path)
			assert.NotEqual(t, http.StatusUnauthorized, w.Code, "admin token must not be rejected by authMiddleware on %s %s", tc.method, tc.path)
		})
	}
}

// TestDriverVehiclesRoute_Wiring verifies GET /api/v1/vehicles requires
// authentication (401 with no token) and accepts any authenticated driver
// (200 with a driver-role token) — no admin role required, unlike the
// /api/v1/admin/* routes above.
func TestDriverVehiclesRoute_Wiring(t *testing.T) {
	driverToken, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	mux := newMux(&noopStore{}, nil, nil, testSecret, time.Time{}, nil, false)

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"driver token", "Bearer " + driverToken, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}
