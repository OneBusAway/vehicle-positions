package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// riderEnvKeys is every environment variable riderConfigFromEnv reads. The
// config test clears them all first so an ambient value in the developer's
// shell cannot make an assertion pass or fail by accident.
var riderEnvKeys = []string{
	"RIDER_MODE_ENABLED", "GTFS_STATIC_URL", "GTFS_STATIC_REFRESH", "TRUSTED_GTFS_RT_URLS",
	"TRUSTED_FEED_POLL", "TRUSTED_FEED_MAX_AGE", "RIDER_JWT_TTL", "RIDER_POINT_RETENTION",
	"RIDER_MAX_SHAPE_DISTANCE", "RIDER_MAX_SPEED", "RIDER_SCHEDULE_EARLY", "RIDER_SCHEDULE_LATE",
	"RIDER_POINT_MAX_AGE",
}

func TestRiderConfigFromEnv(t *testing.T) {
	for _, k := range riderEnvKeys {
		t.Setenv(k, "")
	}

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
	assert.Equal(t, 15*time.Minute, cfg.Thresholds.ScheduleEarly)
	assert.Equal(t, 90*time.Second, cfg.Thresholds.PointMaxAge)
}

func TestRiderConfigFromEnv_RejectsNonPositiveValues(t *testing.T) {
	for _, k := range riderEnvKeys {
		t.Setenv(k, "")
	}
	t.Setenv("RIDER_MODE_ENABLED", "true")
	t.Setenv("GTFS_STATIC_URL", "rider/testdata/fixture.zip")
	// Every one of these would disable the check it configures rather than
	// tighten it, so each falls back to its default.
	t.Setenv("RIDER_MAX_SHAPE_DISTANCE", "0")
	t.Setenv("RIDER_MAX_SPEED", "-1")
	t.Setenv("RIDER_POINT_MAX_AGE", "0s")
	t.Setenv("TRUSTED_FEED_MAX_AGE", "-1m")
	t.Setenv("RIDER_JWT_TTL", "0")

	cfg, err := riderConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, 60.0, cfg.Thresholds.MaxShapeDistance)
	assert.Equal(t, 35.0, cfg.Thresholds.MaxSpeed)
	assert.Equal(t, 90*time.Second, cfg.Thresholds.PointMaxAge)
	assert.Equal(t, defaultTrustedMaxAge, cfg.TrustedMaxAge)
	assert.Equal(t, defaultRiderJWTTTL, cfg.JWTTTL)
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

// TestReapedRidesArePersistedThroughFinishRide covers the path the reap ticker
// takes: Reap ends its sessions in place and leaves them registered, so each
// one is filed through finishRide under the reason the session ended with.
func TestReapedRidesArePersistedThroughFinishRide(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1", "start_date": "20260902"})

	// The session's start time comes from the stored ride, which the fake
	// store stamps with the wall clock, so the reap clock follows it.
	reaped := env.svc.agg.Reap(time.Now().Add(20 * time.Minute))
	require.Equal(t, []string{ride.RideID}, reaped)

	// The reason passed in is not the one recorded: the session ended itself.
	_, err := env.svc.finishRide(context.Background(), ride.RideID, rider.EndIdle)
	require.NoError(t, err)
	stored := env.store.rides[ride.RideID]
	assert.Equal(t, "ended", stored.Status)
	assert.Equal(t, "idle", stored.EndReason)

	// Filed and unregistered: a later sweep has nothing left to retry.
	assert.Empty(t, env.svc.agg.Reap(time.Now().Add(21*time.Minute)))
	_, err = env.svc.finishRide(context.Background(), ride.RideID, rider.EndIdle)
	assert.ErrorIs(t, err, errRideNotActive)
}

// TestReapedRideIsRetriedWhenTheStoreFails pins the retry: a ride whose
// outcome could not be written stays registered and ended, so the next sweep
// returns it again and the write is tried once more.
func TestReapedRideIsRetriedWhenTheStoreFails(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1", "start_date": "20260902"})

	require.Equal(t, []string{ride.RideID}, env.svc.agg.Reap(time.Now().Add(20*time.Minute)))
	env.store.failNext = errors.New("write failed")
	_, err := env.svc.finishRide(context.Background(), ride.RideID, rider.EndIdle)
	require.Error(t, err)
	assert.Equal(t, "active", env.store.rides[ride.RideID].Status, "nothing was written")

	// Still registered and still ended, so the next sweep offers it again.
	require.Equal(t, []string{ride.RideID}, env.svc.agg.Reap(time.Now().Add(21*time.Minute)))
	_, err = env.svc.finishRide(context.Background(), ride.RideID, rider.EndIdle)
	require.NoError(t, err)
	assert.Equal(t, "ended", env.store.rides[ride.RideID].Status)
	assert.Equal(t, "idle", env.store.rides[ride.RideID].EndReason)
}

func TestMain_GTFSFixtureExists(t *testing.T) {
	_, err := os.Stat("rider/testdata/fixture.zip")
	require.NoError(t, err)
}
