package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestRiderConfigFromEnv(t *testing.T) {
	t.Setenv("RIDER_MODE_ENABLED", "false")
	cfg, err := riderConfigFromEnv()
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)

	t.Setenv("RIDER_MODE_ENABLED", "true")
	t.Setenv("GTFS_STATIC_URL", "")
	_, err = riderConfigFromEnv()
	assert.Error(t, err, "GTFS_STATIC_URL required when enabled")

	t.Setenv("GTFS_STATIC_URL", "rider/testdata/fixture.zip")
	t.Setenv("TRUSTED_GTFS_RT_URLS", "http://a/vp.pb, http://b/vp.pb")
	t.Setenv("RIDER_MAX_SHAPE_DISTANCE", "80")
	t.Setenv("RIDER_MAX_SPEED", "garbage")
	t.Setenv("RIDER_SCHEDULE_LATE", "2h")
	cfg, err = riderConfigFromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, []string{"http://a/vp.pb", "http://b/vp.pb"}, cfg.TrustedURLs)
	assert.Equal(t, 80.0, cfg.Thresholds.MaxShapeDistance)
	assert.Equal(t, 35.0, cfg.Thresholds.MaxSpeed, "invalid float falls back to the default")
	assert.Equal(t, 2*time.Hour, cfg.Thresholds.ScheduleLate)
	assert.Equal(t, 24*time.Hour, cfg.GTFSRefresh)
	assert.Equal(t, 30*time.Second, cfg.TrustedPoll)
	assert.Equal(t, 5*time.Minute, cfg.TrustedMaxAge)
	assert.Equal(t, 8760*time.Hour, cfg.JWTTTL)
	assert.Equal(t, 168*time.Hour, cfg.PointRetention)
}

func TestNewRiderRuntime_LoadsIndexAndEndsStaleRides(t *testing.T) {
	store := newFakeRiderStore()
	r, _, _ := store.RegisterRider(context.Background(), "inst", "ios", "x", "1")
	require.NoError(t, store.StartRide(context.Background(), &Ride{ID: "stale", RiderID: r.ID, TripID: "T1", StartDate: "20260902"}))

	cfg := riderConfig{Enabled: true, GTFSSource: "rider/testdata/fixture.zip", GTFSRefresh: time.Hour, TrustedPoll: time.Hour,
		TrustedMaxAge: 5 * time.Minute, JWTTTL: time.Hour, PointRetention: time.Hour, Thresholds: rider.DefaultThresholds()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := newRiderRuntime(ctx, cfg, store, testSecret, false)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, "ended", store.rides["stale"].Status)
	assert.Equal(t, "server_restart", store.rides["stale"].EndReason)
	assert.Equal(t, 3, rt.refresher.Current().Stats().Trips)
	assert.False(t, rt.trusted.Configured())

	cfg.GTFSSource = "does/not/exist.zip"
	_, err = newRiderRuntime(ctx, cfg, store, testSecret, false)
	assert.Error(t, err)
}

func TestRiderRoutes_NotRegisteredWhenDisabled(t *testing.T) {
	mux := newMux(&noopStore{}, nil, nil, testSecret, time.Time{}, nil, false, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/rider/register", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	adminTok, _ := generateJWT(&User{ID: 1, Email: "a@test.com", Role: "admin"}, testSecret)
	req := httptest.NewRequest("GET", "/api/v1/admin/rider/status", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"enabled":false}`, w.Body.String())
}

func TestRiderRoutes_RoleIsolation(t *testing.T) {
	env := newRiderTestEnv(t)
	tracker := NewTracker(time.Minute)
	defer tracker.Stop()
	mux := newMux(&noopStore{}, tracker, nil, testSecret, time.Time{}, nil, false, env.svc)
	_, riderTok := env.register(t)
	driverTok, _ := generateJWT(&User{ID: 1, Email: "d@test.com", Role: "driver"}, testSecret)
	adminTok, _ := generateJWT(&User{ID: 2, Email: "a@test.com", Role: "admin"}, testSecret)

	call := func(method, path, tok string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}
	assert.Equal(t, http.StatusForbidden, call("POST", "/api/v1/locations", riderTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/admin/status", riderTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/admin/rider/status", riderTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/rider/trips/T1/status", driverTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/rider/trips/T1/status", adminTok))
	assert.Equal(t, http.StatusForbidden, call("GET", "/api/v1/admin/rider/rides", driverTok))
	assert.Equal(t, http.StatusOK, call("GET", "/api/v1/admin/rider/rides", adminTok))
	assert.Equal(t, http.StatusOK, call("GET", "/api/v1/rider/trips/T1/status", riderTok))
}

// TestFileReaped_PersistsAnAlreadyRemovedSession covers the path the reap
// ticker takes: Reap ends and unregisters a session before handing it over, so
// finishRide would find nothing and fileReaped has to persist the loose
// session itself. Filing the same session twice is a no-op, not an error.
func TestFileReaped_PersistsAnAlreadyRemovedSession(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1", "start_date": "20260902"})

	// The session's start time comes from the stored ride, which the fake
	// store stamps with the wall clock, so the reap clock follows it.
	reaped := env.svc.agg.Reap(time.Now().Add(20 * time.Minute))
	require.Len(t, reaped, 1)
	require.Equal(t, ride.RideID, reaped[0].ID())

	require.NoError(t, env.svc.fileReaped(context.Background(), reaped[0]))
	stored := env.store.rides[ride.RideID]
	assert.Equal(t, "ended", stored.Status)
	assert.Equal(t, "idle", stored.EndReason)

	assert.NoError(t, env.svc.fileReaped(context.Background(), reaped[0]), "a ride already filed is not an error")
}

func TestMain_GTFSFixtureExists(t *testing.T) {
	_, err := os.Stat("rider/testdata/fixture.zip")
	require.NoError(t, err)
}
