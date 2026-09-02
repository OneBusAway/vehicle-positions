package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// fakeRiderStore is an in-memory riderStore for handler tests.
type fakeRiderStore struct {
	mu       sync.Mutex
	riders   map[string]*Rider // by id
	byInst   map[string]string // installation → id
	rides    map[string]*Ride
	points   map[string][]RidePointRecord
	failNext error // returned once by the next mutating call
}

func newFakeRiderStore() *fakeRiderStore {
	return &fakeRiderStore{riders: map[string]*Rider{}, byInst: map[string]string{}, rides: map[string]*Ride{}, points: map[string][]RidePointRecord{}}
}

func (f *fakeRiderStore) takeErr() error { e := f.failNext; f.failNext = nil; return e }

func (f *fakeRiderStore) RegisterRider(_ context.Context, inst, platform, appID, appVersion string) (*Rider, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeErr(); err != nil {
		return nil, false, err
	}
	if id, ok := f.byInst[inst]; ok {
		r := f.riders[id]
		r.AppVersion = appVersion
		r.LastSeenAt = time.Now()
		return r, false, nil
	}
	r := &Rider{ID: uuid.NewString(), InstallationID: inst, Platform: platform, AppID: appID, AppVersion: appVersion, Tier: "new", CreatedAt: time.Now(), LastSeenAt: time.Now()}
	f.riders[r.ID] = r
	f.byInst[inst] = r.ID
	return r, true, nil
}

func (f *fakeRiderStore) GetRider(_ context.Context, id string) (*Rider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.riders[id]; ok {
		return r, nil
	}
	return nil, ErrRiderNotFound
}

func (f *fakeRiderStore) StartRide(_ context.Context, ride *Ride) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeErr(); err != nil {
		return err
	}
	ride.Status, ride.State, ride.StartedAt = "active", "pending", time.Now()
	cp := *ride
	f.rides[ride.ID] = &cp
	return nil
}

func (f *fakeRiderStore) RecordRidePoints(_ context.Context, rideID, riderID string, pts []RidePointRecord, p RideProgress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeErr(); err != nil {
		return err
	}
	r, ok := f.rides[rideID]
	if !ok {
		return ErrRideNotFound
	}
	f.points[rideID] = append(f.points[rideID], pts...)
	r.State, r.Corroborated, r.PointsTotal, r.PointsMatched, r.PointsCorroborated, r.PointsContradicted = p.State, p.Corroborated, p.PointsTotal, p.PointsMatched, p.PointsCorroborated, p.PointsContradicted
	return nil
}

func (f *fakeRiderStore) FinishRide(_ context.Context, rideID string, o RideOutcome) (*Rider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeErr(); err != nil {
		return nil, err
	}
	r, ok := f.rides[rideID]
	if !ok || r.Status != "active" {
		return nil, ErrRideNotFound
	}
	now := time.Now()
	r.Status, r.EndedAt, r.EndReason = "ended", &now, o.EndReason
	r.State, r.Corroborated = o.Progress.State, o.Progress.Corroborated
	r.PointsTotal, r.PointsMatched, r.PointsCorroborated, r.PointsContradicted = o.Progress.PointsTotal, o.Progress.PointsMatched, o.Progress.PointsCorroborated, o.Progress.PointsContradicted
	rd := f.riders[r.RiderID]
	rd.Score = rider.Clamp(rd.Score + o.ScoreDelta)
	rd.Tier = string(rider.TierFor(rd.Score))
	rd.RidesTotal++
	if o.Rejected {
		rd.RidesRejected++
	}
	if o.Corroborated {
		rd.RidesCorroborated++
	}
	return rd, nil
}

func (f *fakeRiderStore) EndAllActiveRides(_ context.Context, reason string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.rides {
		if r.Status == "active" {
			r.Status, r.EndReason = "ended", reason
			n++
		}
	}
	return n, nil
}

func (f *fakeRiderStore) ListRides(_ context.Context, status string, limit, offset int) ([]Ride, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Ride
	for _, r := range f.rides {
		if r.Status == status {
			out = append(out, *r)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRiderStore) CountRidersByTier(_ context.Context) (map[string]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]int{}
	for _, r := range f.riders {
		m[r.Tier]++
	}
	return m, nil
}

func (f *fakeRiderStore) DeleteRidePointsBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// fakeTrusted is a scriptable trustedLookup.
type fakeTrusted struct {
	mu       sync.Mutex
	vehicles map[rider.TripKey]rider.TrustedVehicle
	health   []rider.FeedHealth
}

func newFakeTrusted() *fakeTrusted {
	return &fakeTrusted{vehicles: map[rider.TripKey]rider.TrustedVehicle{}}
}

func (f *fakeTrusted) Configured() bool { return true }

func (f *fakeTrusted) Lookup(k rider.TripKey, _ time.Time) (rider.TrustedVehicle, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vehicles[k]
	return v, ok
}

func (f *fakeTrusted) Covers(k rider.TripKey, now time.Time) bool {
	_, ok := f.Lookup(k, now)
	return ok
}

func (f *fakeTrusted) Health() []rider.FeedHealth { return f.health }

func (f *fakeTrusted) set(k rider.TripKey, v rider.TrustedVehicle) {
	f.mu.Lock()
	f.vehicles[k] = v
	f.mu.Unlock()
}

func (f *fakeTrusted) clear(k rider.TripKey) {
	f.mu.Lock()
	delete(f.vehicles, k)
	f.mu.Unlock()
}

// newPermissiveRegistrationLimiter returns a registration limiter whose limit
// never trips, so tests that are not about rate limiting never hit one. It has
// no cleanup goroutine: nothing accumulates in a limit that never rejects.
func newPermissiveRegistrationLimiter() *RegistrationRateLimiter {
	return &RegistrationRateLimiter{
		byIP:  make(map[string]*loginWindowEntry),
		limit: 1 << 30,
		stop:  make(chan struct{}),
	}
}

// riderTestEnv wires a riderService against the fixture GTFS, a fake store,
// and a fake trusted feed, with a controllable clock.
type riderTestEnv struct {
	svc     *riderService
	store   *fakeRiderStore
	trusted *fakeTrusted
	index   *rider.Index
	mux     *http.ServeMux
	now     time.Time
}

func loadFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	b, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	static, err := rider.ParseStaticBytes(b)
	require.NoError(t, err)
	ix, err := rider.BuildIndex(static, "fixture", time.Now())
	require.NoError(t, err)
	return ix
}

func newRiderTestEnv(t *testing.T) *riderTestEnv {
	t.Helper()
	ix := loadFixtureIndex(t)
	env := &riderTestEnv{store: newFakeRiderStore(), trusted: newFakeTrusted(), index: ix}
	env.now = time.Date(2026, 9, 2, 8, 0, 0, 0, ix.Timezone())
	agg := rider.NewAggregator(rider.DefaultThresholds(), ix.Timezone())
	env.svc = newRiderService(env.store, agg, func() *rider.Index { return ix }, env.trusted, testSecret, time.Hour, false, rider.DefaultThresholds())
	env.svc.now = func() time.Time { return env.now }
	// Permissive limiters by default so unrelated tests never trip them; the
	// rate-limit tests install strict ones explicitly.
	env.svc.regLimiter.Stop()
	env.svc.batchLimiter.Stop()
	env.svc.regLimiter = newPermissiveRegistrationLimiter()
	env.svc.batchLimiter = NewKeyedRateLimiter(time.Millisecond, 1_000)
	t.Cleanup(env.svc.Stop)
	env.mux = http.NewServeMux()
	registerRiderRoutes(env.mux, env.svc)
	return env
}

func (e *riderTestEnv) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func (e *riderTestEnv) register(t *testing.T) (riderID, token string) {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/rider/register", "", map[string]any{"installation_id": uuid.NewString(), "platform": "ios", "app_id": "org.test", "app_version": "1"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp riderRegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp.RiderID, resp.Token
}

func (e *riderTestEnv) startRide(t *testing.T, token string, body map[string]any) startRideResponse {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/rider/rides", token, body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp startRideResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

func TestRiderRegister(t *testing.T) {
	env := newRiderTestEnv(t)
	inst := uuid.NewString()
	body := map[string]any{"installation_id": inst, "platform": "ios", "app_id": "org.test", "app_version": "1.0", "attestation": nil}
	w := env.do(t, "POST", "/api/v1/rider/register", "", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var first riderRegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&first))
	assert.NotEmpty(t, first.RiderID)
	assert.Equal(t, 5, first.ReportIntervalSeconds)
	assert.Equal(t, 12, first.MaxBatchSize)
	claims, err := parseSessionToken(first.Token, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "rider", claims["role"])
	assert.Equal(t, first.RiderID, claims["sub"])

	w = env.do(t, "POST", "/api/v1/rider/register", "", body)
	assert.Equal(t, http.StatusOK, w.Code, "re-registration returns 200")
	var second riderRegisterResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&second))
	assert.Equal(t, first.RiderID, second.RiderID)

	bad := []map[string]any{
		{"installation_id": "not-a-uuid", "platform": "ios"},
		{"installation_id": uuid.NewString(), "platform": "windows"},
		{"installation_id": uuid.NewString(), "platform": "ios", "attestation": map[string]any{"x": 1}},
		{"installation_id": uuid.NewString(), "platform": "ios", "unknown": 1},
	}
	for i, b := range bad {
		w := env.do(t, "POST", "/api/v1/rider/register", "", b)
		assert.Equal(t, http.StatusBadRequest, w.Code, "case %d: %s", i, w.Body.String())
	}

	req := httptest.NewRequest("POST", "/api/v1/rider/register", bytes.NewBufferString("{}"))
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestRiderRegister_RateLimited(t *testing.T) {
	env := newRiderTestEnv(t)
	env.svc.regLimiter = NewRegistrationRateLimiter()
	for i := 0; i < registrationIPLimit; i++ {
		env.register(t)
	}
	w := env.do(t, "POST", "/api/v1/rider/register", "", map[string]any{"installation_id": uuid.NewString(), "platform": "ios"})
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestStartRide(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)

	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1", "start_date": "20260902", "route_id": "R1", "destination_stop_id": "ST3"})
	assert.NotEmpty(t, resp.RideID)
	assert.Equal(t, "pending", resp.State)
	assert.Equal(t, 5, resp.ReportIntervalSeconds)
	require.NotNil(t, resp.Destination)
	assert.Equal(t, "ST3", resp.Destination.StopID)
	assert.InDelta(t, 47.609, resp.Destination.Latitude, 0.001)
	assert.Equal(t, 1, env.svc.agg.ActiveCount())
	stored := env.store.rides[resp.RideID]
	require.NotNil(t, stored)
	assert.Equal(t, "R1", stored.RouteID)
	assert.Equal(t, "ST3", stored.DestinationStopID)

	// Default start_date is the service date; no destination when unknown stop.
	resp2 := env.startRide(t, tok, map[string]any{"trip_id": "T1", "destination_stop_id": "NOPE"})
	assert.Nil(t, resp2.Destination)
	assert.Equal(t, "20260902", env.store.rides[resp2.RideID].StartDate)
	assert.Equal(t, "ended", env.store.rides[resp.RideID].Status, "first ride superseded")
	assert.Equal(t, "superseded", env.store.rides[resp.RideID].EndReason)
	assert.Equal(t, 1, env.svc.agg.ActiveCount())

	cases := []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"trip_id": "NOPE"}, http.StatusNotFound},
		{map[string]any{"trip_id": "T1", "start_date": "20260905"}, http.StatusUnprocessableEntity}, // Saturday
		{map[string]any{"trip_id": "T1", "route_id": "R2"}, http.StatusUnprocessableEntity},
		{map[string]any{"trip_id": ""}, http.StatusBadRequest},
		{map[string]any{"trip_id": "T1", "start_date": "2026-09-02"}, http.StatusBadRequest},
	}
	for i, tc := range cases {
		w := env.do(t, "POST", "/api/v1/rider/rides", tok, tc.body)
		assert.Equal(t, tc.want, w.Code, "case %d: %s", i, w.Body.String())
	}

	w := env.do(t, "POST", "/api/v1/rider/rides", "", map[string]any{"trip_id": "T1"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestEndRide(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	_, otherTok := env.register(t)
	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	w := env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", otherTok, map[string]any{"reason": "user_requested"})
	assert.Equal(t, http.StatusNotFound, w.Code, "another rider's ride looks nonexistent")

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "off_route"})
	assert.Equal(t, http.StatusBadRequest, w.Code, "server-only reason")

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var er endRideResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&er))
	assert.Equal(t, "ride ended", er.Status)
	assert.Equal(t, 0, er.Summary.Points)
	assert.Equal(t, "ended", env.store.rides[resp.RideID].Status)
	assert.Equal(t, "arrived", env.store.rides[resp.RideID].EndReason)
	assert.Equal(t, 0, env.svc.agg.ActiveCount())
	assert.Equal(t, 1, env.store.riders[riderID].RidesTotal)

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusConflict, w.Code)
	w = env.do(t, "POST", "/api/v1/rider/rides/not-a-uuid/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestFinishRide_StoreFailureKeepsSession(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.store.failNext = assert.AnError
	w := env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, 1, env.svc.agg.ActiveCount(), "session stays so reaping can retry")
	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, env.svc.agg.ActiveCount())
}
