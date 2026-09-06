package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	ix, err := rider.LoadIndex(t.Context(), "rider/testdata/fixture.zip", nil, time.Now())
	require.NoError(t, err)
	return ix
}

func newRiderTestEnv(t *testing.T) *riderTestEnv {
	t.Helper()
	ix := loadFixtureIndex(t)
	env := &riderTestEnv{store: newFakeRiderStore(), trusted: newFakeTrusted(), index: ix}
	env.now = time.Date(2026, 9, 2, 8, 0, 0, 0, ix.Timezone())
	agg := rider.NewAggregator(rider.DefaultThresholds(), ix.Timezone())
	env.svc = newRiderService(env.store, agg, func() *rider.Index { return ix }, env.trusted, testSecret, time.Hour, false)
	env.svc.now = func() time.Time { return env.now }
	// Permissive limiters by default so unrelated tests never trip them; the
	// rate-limit tests install strict ones explicitly.
	env.svc.regLimiter.Stop()
	env.svc.batchLimiter.Stop()
	env.svc.regLimiter = newPermissiveRegistrationLimiter()
	env.svc.batchLimiter = NewKeyedRateLimiter(time.Millisecond, 1_000, true)
	env.svc.rideLimiter.Stop()
	env.svc.rideLimiter = NewKeyedRateLimiter(time.Millisecond, 1_000, true)
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

	// Bodies the decoder must reject outright. The oversized one is checked by
	// its message too: the body cap, not the app_version length rule, is what
	// has to stop it.
	raw := []struct {
		name, body, contains string
		code                 int
	}{
		{"trailing data", `{"installation_id":"` + uuid.NewString() + `","platform":"ios"}{"more":1}`, "single JSON object", http.StatusBadRequest},
		{"body past the cap", `{"installation_id":"` + uuid.NewString() + `","platform":"ios","app_version":"` + strings.Repeat("x", riderMaxBodyBytes) + `"}`, "too large", http.StatusRequestEntityTooLarge},
		{"wrong field type", `{"installation_id":42,"platform":"ios"}`, "has invalid type", http.StatusBadRequest},
	}
	for _, tc := range raw {
		req := httptest.NewRequest("POST", "/api/v1/rider/register", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		env.mux.ServeHTTP(w, req)
		assert.Equal(t, tc.code, w.Code, tc.name)
		assert.Contains(t, w.Body.String(), tc.contains, tc.name)
		assert.NotContains(t, w.Body.String(), "Go struct", tc.name)
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

// TestFinishRide_StoreFailureLeavesRideForTheReaper: the session is ended
// before the outcome is written, so a failed write leaves it ended but
// registered — over as far as the client and the feed are concerned, and
// filed, with its own reason, by the reaper's next sweep.
func TestFinishRide_StoreFailureLeavesRideForTheReaper(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.store.failNext = assert.AnError
	w := env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	snap, ok := env.svc.agg.Snapshot(resp.RideID)
	require.True(t, ok, "the session stays registered until its outcome is written")
	assert.True(t, snap.Ended)
	assert.Equal(t, rider.EndArrived, snap.Summary.EndReason)
	assert.Equal(t, 0, env.svc.agg.ActiveCount(), "but it is no longer active")
	assert.Equal(t, "active", env.store.rides[resp.RideID].Status, "the store has not heard yet")

	w = env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/positions", tok, map[string]any{"positions": []map[string]any{
		{"latitude": 47.6, "longitude": -122.33, "timestamp": env.now.Unix()},
	}})
	assert.Equal(t, http.StatusConflict, w.Code, "nothing more can be folded into an ended ride")

	reaped := env.svc.agg.Reap(env.now)
	require.Equal(t, []string{resp.RideID}, reaped)
	_, err := env.svc.finishRide(context.Background(), resp.RideID, rider.EndIdle)
	require.NoError(t, err)
	assert.Equal(t, "ended", env.store.rides[resp.RideID].Status)
	assert.Equal(t, "arrived", env.store.rides[resp.RideID].EndReason, "the session's own reason, not the reaper's")
	_, ok = env.svc.agg.Snapshot(resp.RideID)
	assert.False(t, ok, "filed and gone")
}

func TestFinishRide_AlreadyEndedInStoreDropsSession(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	resp := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	// The row ended behind the server's back (a restart's EndAllActiveRides,
	// say). The session is stale, not retryable.
	env.store.rides[resp.RideID].Status = "ended"

	w := env.do(t, "POST", "/api/v1/rider/rides/"+resp.RideID+"/end", tok, map[string]any{"reason": "arrived"})
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, 0, env.svc.agg.ActiveCount(), "the stale session is dropped rather than retried forever")
}

// walkPoints returns n on-schedule T1 uploads starting `along` metres in,
// 10 m and 6 s apart, in wire form. It advances env.now past the last point.
func (e *riderTestEnv) walkPoints(along float64, n int) []map[string]any {
	trip, _ := e.index.Trip("T1")
	base := time.Date(2026, 9, 2, 8, 0, 0, 0, e.index.Timezone())
	var out []map[string]any
	var last time.Time
	for i := 0; i < n; i++ {
		a := along + float64(i*10)
		ts := base.Add(time.Duration(a/1001*600) * time.Second)
		p := trip.Shape.PointAt(a)
		out = append(out, map[string]any{"latitude": p.Lat, "longitude": p.Lon, "accuracy": 5, "speed": 1.7, "bearing": 0, "timestamp": ts.Unix()})
		last = ts
	}
	e.now = last.Add(2 * time.Second)
	return out
}

func (e *riderTestEnv) upload(t *testing.T, token, rideID string, pts []map[string]any) (positionsResponse, int) {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/rider/rides/"+rideID+"/positions", token, map[string]any{"positions": pts})
	var resp positionsResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return resp, w.Code
}

func TestPositions_VerifiesAndPersists(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	resp, code := env.upload(t, tok, ride.RideID, env.walkPoints(0, 5))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "verified", resp.State)
	assert.Equal(t, 5, resp.Accepted)
	assert.False(t, resp.Published)
	assert.Equal(t, "unavailable", resp.Corroboration)
	assert.False(t, resp.Ended)
	assert.Len(t, env.store.points[ride.RideID], 5)
	assert.Equal(t, "verified", env.store.rides[ride.RideID].State)
	assert.Equal(t, 5, env.store.rides[ride.RideID].PointsMatched)
}

func TestPositions_CorroborationAndPublish(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	trip, _ := env.index.Trip("T1")
	pts := env.walkPoints(0, 12)
	env.trusted.set(rider.TripKey{TripID: "T1", StartDate: "20260902"}, rider.TrustedVehicle{VehicleID: "bus", Pos: trip.Shape.PointAt(60), Timestamp: env.now})
	resp, code := env.upload(t, tok, ride.RideID, pts)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "corroborated", resp.Corroboration)
	assert.True(t, resp.Published)
	assert.True(t, env.store.rides[ride.RideID].Corroborated)
	assert.Equal(t, 12, env.store.rides[ride.RideID].PointsCorroborated)

	// Trusted feed covers the trip → not in estimates; drop it → estimate appears.
	assert.Empty(t, env.svc.Estimates(env.now))
	env.trusted.clear(rider.TripKey{TripID: "T1", StartDate: "20260902"})
	est := env.svc.Estimates(env.now)
	require.Len(t, est, 1)
	assert.Equal(t, "T1", est[0].Key.TripID)
}

func TestPositions_RejectionEndsRideAndPenalises(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	pts := env.walkPoints(0, 6)
	for i := range pts {
		pts[i]["longitude"] = -122.3200 // ~750 m east of the shape
	}
	resp, code := env.upload(t, tok, ride.RideID, pts)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ended)
	assert.Equal(t, "off_route", resp.EndReason)
	assert.Equal(t, "rejected", resp.State)
	assert.Equal(t, 5, resp.Accepted)
	assert.Equal(t, 1, resp.Ignored)
	assert.Equal(t, "ended", env.store.rides[ride.RideID].Status)
	assert.Equal(t, -1, env.store.riders[riderID].Score)

	_, code = env.upload(t, tok, ride.RideID, env.walkPoints(0, 1))
	assert.Equal(t, http.StatusConflict, code)
}

func TestPositions_Validation(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	_, otherTok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})

	_, code := env.upload(t, tok, ride.RideID, nil)
	assert.Equal(t, http.StatusBadRequest, code, "empty batch")
	_, code = env.upload(t, tok, ride.RideID, env.walkPoints(0, 13))
	assert.Equal(t, http.StatusBadRequest, code, "batch too large")
	_, code = env.upload(t, tok, ride.RideID, []map[string]any{{"latitude": 1, "longitude": 1}})
	assert.Equal(t, http.StatusBadRequest, code, "missing timestamp")
	_, code = env.upload(t, tok, ride.RideID, []map[string]any{{"latitude": 91.0, "longitude": 0, "timestamp": env.now.Unix()}})
	assert.Equal(t, http.StatusBadRequest, code, "coordinates off the globe")
	_, code = env.upload(t, otherTok, ride.RideID, env.walkPoints(0, 1))
	assert.Equal(t, http.StatusNotFound, code, "someone else's ride")
	_, code = env.upload(t, tok, "not-a-uuid", env.walkPoints(0, 1))
	assert.Equal(t, http.StatusNotFound, code)
	_, code = env.upload(t, tok, uuid.NewString(), env.walkPoints(0, 1))
	assert.Equal(t, http.StatusConflict, code, "unknown ride is treated as ended")

	// Ignored points are not persisted.
	resp, code := env.upload(t, tok, ride.RideID, []map[string]any{{"latitude": 47.6, "longitude": -122.33, "accuracy": 500, "timestamp": env.now.Unix()}})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, resp.Ignored)
	assert.Empty(t, env.store.points[ride.RideID])

	// A reading the device never made is stored as absent, not as a zero.
	bare := env.walkPoints(0, 1)[0]
	delete(bare, "accuracy")
	delete(bare, "speed")
	delete(bare, "bearing")
	resp, code = env.upload(t, tok, ride.RideID, []map[string]any{bare})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 1, resp.Accepted)
	require.Len(t, env.store.points[ride.RideID], 1)
	rec := env.store.points[ride.RideID][0]
	assert.Equal(t, "matched", rec.Outcome)
	assert.Equal(t, "unavailable", rec.Corroboration)
	assert.Nil(t, rec.Accuracy)
	assert.Nil(t, rec.Speed)
	assert.Nil(t, rec.Bearing)
}

func TestPositions_RateLimitedPerRider(t *testing.T) {
	env := newRiderTestEnv(t)
	env.svc.batchLimiter = NewKeyedRateLimiter(2*time.Second, 2, true)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	_, c1 := env.upload(t, tok, ride.RideID, env.walkPoints(0, 1))
	_, c2 := env.upload(t, tok, ride.RideID, env.walkPoints(10, 1))
	_, c3 := env.upload(t, tok, ride.RideID, env.walkPoints(20, 1))
	assert.Equal(t, http.StatusOK, c1)
	assert.Equal(t, http.StatusOK, c2)
	assert.Equal(t, http.StatusTooManyRequests, c3, "burst of 2 per 2 s")
}

func TestPositions_BlockedRiderShadowed(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	env.store.riders[riderID].Tier = "blocked"
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	resp, code := env.upload(t, tok, ride.RideID, env.walkPoints(0, 5))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "verified", resp.State, "verdicts look normal")
	assert.False(t, resp.Published)
	assert.Empty(t, env.store.points[ride.RideID], "nothing persisted for blocked riders")
}

func TestPositions_StoreFailureReturns500(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	env.store.failNext = assert.AnError
	_, code := env.upload(t, tok, ride.RideID, env.walkPoints(0, 3))
	assert.Equal(t, http.StatusInternalServerError, code)
	resp, code := env.upload(t, tok, ride.RideID, env.walkPoints(30, 1))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "verified", resp.State, "in-memory session advanced despite the failed commit")
	assert.Equal(t, 4, env.store.rides[ride.RideID].PointsTotal, "absolute counters heal the record")
}

func TestPositions_StoreFailureStillFilesAnEndedRide(t *testing.T) {
	env := newRiderTestEnv(t)
	riderID, tok := env.register(t)
	ride := env.startRide(t, tok, map[string]any{"trip_id": "T1"})
	pts := env.walkPoints(0, 6)
	for i := range pts {
		pts[i]["longitude"] = -122.3200 // ~750 m east of the shape
	}
	// failNext is consumed by the first mutating call — recording the points —
	// so filing the ride afterwards succeeds. The rider is told the batch was
	// not stored; the ride is over either way.
	env.store.failNext = assert.AnError
	_, code := env.upload(t, tok, ride.RideID, pts)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Equal(t, "ended", env.store.rides[ride.RideID].Status, "the engine ended it, so it is filed")
	assert.Equal(t, "off_route", env.store.rides[ride.RideID].EndReason)
	assert.Equal(t, -1, env.store.riders[riderID].Score)
	assert.Equal(t, 0, env.svc.agg.ActiveCount(), "no session left behind for the reaper")
}

func TestTripStatus(t *testing.T) {
	env := newRiderTestEnv(t)
	_, tok := env.register(t)
	w := env.do(t, "GET", "/api/v1/rider/trips/T1/status?start_date=20260902", tok, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var st tripStatusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.Equal(t, tripStatusResponse{TripID: "T1", StartDate: "20260902"}, st)

	env.trusted.set(rider.TripKey{TripID: "T1", StartDate: "20260902"}, rider.TrustedVehicle{VehicleID: "bus", Timestamp: env.now})
	w = env.do(t, "GET", "/api/v1/rider/trips/T1/status", tok, nil)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.True(t, st.Trusted)
	assert.Equal(t, "20260902", st.StartDate, "defaults to the service date")

	assert.Equal(t, http.StatusNotFound, env.do(t, "GET", "/api/v1/rider/trips/NOPE/status", tok, nil).Code)
	assert.Equal(t, http.StatusBadRequest, env.do(t, "GET", "/api/v1/rider/trips/T1/status?start_date=x", tok, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, env.do(t, "GET", "/api/v1/rider/trips/T1/status", "", nil).Code)

	// A trusted rider riding the trip is enough to publish a position on their
	// own, so the trip becomes rider-reported once the feed is out of the way.
	env.trusted.clear(rider.TripKey{TripID: "T1", StartDate: "20260902"})
	riderID, riderTok := env.register(t)
	env.store.riders[riderID].Tier = "trusted"
	ride := env.startRide(t, riderTok, map[string]any{"trip_id": "T1"})
	_, code := env.upload(t, riderTok, ride.RideID, env.walkPoints(0, 5))
	require.Equal(t, http.StatusOK, code)

	st = env.tripStatus(t, tok, "T1")
	assert.True(t, st.RiderReported)
	assert.Equal(t, 1, st.Riders)
	assert.False(t, st.Trusted)

	// The agency's own position wins: a covered trip is never rider-reported,
	// though its riders are still counted.
	env.trusted.set(rider.TripKey{TripID: "T1", StartDate: "20260902"}, rider.TrustedVehicle{VehicleID: "bus", Timestamp: env.now})
	st = env.tripStatus(t, tok, "T1")
	assert.True(t, st.Trusted)
	assert.False(t, st.RiderReported, "the trusted feed speaks for this trip")
	assert.Equal(t, 1, st.Riders)
}

// tripStatus fetches the status of a trip on the current service date.
func (e *riderTestEnv) tripStatus(t *testing.T, token, tripID string) tripStatusResponse {
	t.Helper()
	w := e.do(t, "GET", "/api/v1/rider/trips/"+tripID+"/status", token, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var st tripStatusResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	return st
}
