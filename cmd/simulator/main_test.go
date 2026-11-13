package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocationReportJSONRoundTrip(t *testing.T) {
	report := locationReport{
		VehicleID: "sim-vehicle-001",
		TripID:    "sim-trip-1",
		RouteID:   "sim-route-1",
		StartDate: "20261113",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Bearing:   327.5,
		Speed:     8.0,
		Accuracy:  5.0,
		Timestamp: 1752566400,
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	// Verify JSON field names match server's expected format
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	expectedFields := []string{"vehicle_id", "trip_id", "route_id", "start_date", "latitude", "longitude", "bearing", "speed", "accuracy", "timestamp"}
	for _, field := range expectedFields {
		assert.Contains(t, raw, field, "missing JSON field %q", field)
	}
	assert.Len(t, raw, len(expectedFields), "unexpected extra fields in JSON")

	// Verify round-trip preserves values
	var decoded locationReport
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, report, decoded)
	assert.Equal(t, "sim-route-1", decoded.RouteID)
	assert.Contains(t, string(data), `"route_id":"sim-route-1"`)
}

func TestSendReport_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/locations", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s := &stats{}
	report := &locationReport{
		VehicleID: "test-vehicle",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Bearing:   90.0,
		Speed:     8.0,
		Accuracy:  5.0,
		Timestamp: time.Now().Unix(),
	}

	sendReport(context.Background(), server.Client(), server.URL, "test-vehicle", report, s)

	assert.Equal(t, int64(1), s.succeeded.Load())
	assert.Equal(t, int64(0), s.failed.Load())
	assert.Greater(t, s.totalMS.Load(), int64(-1))
}

func TestSendReport_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid vehicle_id"}`))
	}))
	defer server.Close()

	s := &stats{}
	report := &locationReport{
		VehicleID: "test-vehicle",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Timestamp: time.Now().Unix(),
	}

	sendReport(context.Background(), server.Client(), server.URL, "test-vehicle", report, s)

	assert.Equal(t, int64(0), s.succeeded.Load())
	assert.Equal(t, int64(1), s.failed.Load())
}

func TestSendReport_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s := &stats{}
	report := &locationReport{
		VehicleID: "test-vehicle",
		Latitude:  -1.2864,
		Longitude: 36.8172,
		Timestamp: time.Now().Unix(),
	}

	sendReport(ctx, http.DefaultClient, "http://localhost:99999", "test-vehicle", report, s)

	// Cancelled context should not count as a failure
	assert.Equal(t, int64(0), s.succeeded.Load())
	assert.Equal(t, int64(0), s.failed.Load())
}

func TestLoginReturnsTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/auth/login", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "sim@example.com", body["email"])
		assert.Equal(t, "hunter2", body["password"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-123","refresh_token":"ref-123","expires_in":900}`))
	}))
	defer server.Close()

	tokens, err := login(context.Background(), server.Client(), server.URL, "sim@example.com", "hunter2")
	require.NoError(t, err)
	assert.Equal(t, tokenResponse{AccessToken: "tok-123", RefreshToken: "ref-123", ExpiresIn: 900}, tokens)
}

// A vehicle signed in without a refresh token or a lifetime could not renew
// its access token, so it would stop reporting partway through the run.
func TestLoginRejectsIncompleteTokens(t *testing.T) {
	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"access_token":"a","expires_in":900}`, "no refresh_token"},
		{`{"access_token":"a","refresh_token":"r"}`, "no positive expires_in"},
		{`{"access_token":"a","refresh_token":"r","expires_in":0}`, "no positive expires_in"},
		{`{"access_token":"a","refresh_token":"r","expires_in":-900}`, "no positive expires_in"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := login(t.Context(), server.Client(), server.URL, "sim@example.com", "hunter2")
		server.Close()
		if assert.Error(t, err, tc.body) {
			assert.Contains(t, err.Error(), tc.want, tc.body)
		}
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid email or password"}`))
	}))
	defer server.Close()

	_, err := login(context.Background(), server.Client(), server.URL, "sim@example.com", "wrong")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestLoginRejectsEmptyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := login(context.Background(), server.Client(), server.URL, "sim@example.com", "hunter2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "login response contained no access_token")
}

func TestLoginRejectsDeprecatedTokenOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"old123"}`))
	}))
	defer server.Close()
	tokens, err := login(context.Background(), server.Client(), server.URL, "sim@example.com", "hunter2")
	require.Error(t, err)
	assert.Empty(t, tokens.AccessToken)
}

// The regression this fixes: reports must carry the session token, because
// POST /api/v1/locations sits behind requireAuth and 401s without it.
func TestBearerTransportSetsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := &http.Client{Transport: bearerTransport{token: "tok-123", base: http.DefaultTransport}}
	sendReport(context.Background(), client, server.URL, "sim-vehicle-001", &locationReport{}, &stats{})

	assert.Equal(t, "Bearer tok-123", gotAuth)
}

// authServer stands in for the server's side of token refresh. The driver
// signs in with a1/r1, POST /api/v1/auth/refresh trades r<n> for a<n+1>/r<n+1>
// and refuses any other refresh token, as the real server refuses a spent one,
// and every other request is accepted only with the newest access token
// before it expires. The session under test reads the same clock.
type authServer struct {
	*httptest.Server

	mu            sync.Mutex
	clock         time.Time
	generation    int
	expiresAt     time.Time
	refreshStatus int      // anything other than 200 is answered instead of a rotation
	presented     []string // refresh tokens sent to POST /api/v1/auth/refresh
	received      int      // requests other than refreshes
	accepted      int
}

const testTokenLifetime = 15 * time.Minute

func newAuthServer(t *testing.T) *authServer {
	a := &authServer{clock: time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC), generation: 1, refreshStatus: http.StatusOK}
	a.expiresAt = a.clock.Add(testTokenLifetime)
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.URL.Path != "/api/v1/auth/refresh" {
			a.received++
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer a%d", a.generation) || !a.clock.Before(a.expiresAt) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			a.accepted++
			w.WriteHeader(http.StatusCreated)
			return
		}
		var body map[string]string
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.presented = append(a.presented, body["refresh_token"])
		if a.refreshStatus != http.StatusOK {
			w.WriteHeader(a.refreshStatus)
			return
		}
		if body["refresh_token"] != fmt.Sprintf("r%d", a.generation) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		a.generation++
		a.expiresAt = a.clock.Add(testTokenLifetime)
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  fmt.Sprintf("a%d", a.generation),
			RefreshToken: fmt.Sprintf("r%d", a.generation),
			ExpiresIn:    int(testTokenLifetime.Seconds()),
		})
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *authServer) now() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clock
}

func (a *authServer) advance(d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clock = a.clock.Add(d)
}

func (a *authServer) answerRefreshWith(status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshStatus = status
}

func (a *authServer) counts() (presented []string, received, accepted int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.presented...), a.received, a.accepted
}

// signIn returns the session provisionDrivers would make from the driver's
// login, and the vehicle's client that carries it.
func (a *authServer) signIn() (*session, *http.Client) {
	s := newSession("sim-test-001", a.URL, a.Client(),
		tokenResponse{AccessToken: "a1", RefreshToken: "r1", ExpiresIn: int(testTokenLifetime.Seconds())}, a.now)
	return s, &http.Client{Transport: sessionTransport{session: s, base: http.DefaultTransport}}
}

// TestSessionRefreshesBeforeTheAccessTokenExpires pins what the session is
// for: a vehicle keeps reporting for longer than one access token lasts.
func TestSessionRefreshesBeforeTheAccessTokenExpires(t *testing.T) {
	a := newAuthServer(t)
	_, client := a.signIn()
	st := &stats{}
	report := func() { sendReport(t.Context(), client, a.URL, "sim-test-001", &locationReport{}, st) }

	a.advance(testTokenLifetime - refreshMargin - time.Second)
	report()
	presented, _, _ := a.counts()
	assert.Empty(t, presented, "refreshed earlier than refreshMargin before expiry")

	a.advance(time.Second)
	report()
	presented, _, _ = a.counts()
	assert.Equal(t, []string{"r1"}, presented)

	// The new token's lifetime runs from the refresh, so the next refresh is
	// due a whole lifetime later and presents the rotated refresh token.
	a.advance(testTokenLifetime - refreshMargin - time.Second)
	report()
	presented, _, _ = a.counts()
	assert.Equal(t, []string{"r1"}, presented)

	a.advance(time.Second)
	report()
	presented, received, accepted := a.counts()
	assert.Equal(t, []string{"r1", "r2"}, presented)
	assert.Equal(t, 4, received)
	assert.Equal(t, 4, accepted, "28 minutes in, long after the sign-in token expired, every report is still accepted")
	assert.Equal(t, int64(4), st.succeeded.Load())
	assert.Equal(t, int64(0), st.failed.Load())

	// Starting and ending a trip pass vehicle.token, the token the driver
	// signed in with, to postJSON. It was refreshed away long ago, so the
	// transport has to send the current one in its place.
	require.NoError(t, postJSON(t.Context(), client, a.URL+"/api/v1/trips/start", "a1", map[string]string{}, nil))
}

// A refresh that fails for a reason that may pass (here the per-IP limit)
// must not cost reports while the old token still works, and must be tried
// again rather than given up on.
func TestSessionKeepsItsTokenWhileARefreshFails(t *testing.T) {
	a := newAuthServer(t)
	_, client := a.signIn()
	st := &stats{}
	report := func() { sendReport(t.Context(), client, a.URL, "sim-test-001", &locationReport{}, st) }

	a.answerRefreshWith(http.StatusTooManyRequests)
	a.advance(testTokenLifetime - refreshMargin)
	report()
	a.advance(refreshMargin / 2)
	report()
	presented, received, accepted := a.counts()
	assert.Equal(t, []string{"r1", "r1"}, presented, "each request inside the margin tries the refresh again")
	assert.Equal(t, 2, received)
	assert.Equal(t, 2, accepted, "both reports went out with a1, which had not expired")

	// a1 expires. A report that cannot get a new token is not sent with the
	// expired one.
	a.advance(refreshMargin / 2)
	report()
	presented, received, accepted = a.counts()
	assert.Len(t, presented, 3)
	assert.Equal(t, 2, received, "a report was sent with an expired token")
	assert.Equal(t, 2, accepted)
	assert.Equal(t, int64(1), st.failed.Load())

	a.answerRefreshWith(http.StatusOK)
	report()
	presented, received, accepted = a.counts()
	assert.Equal(t, []string{"r1", "r1", "r1", "r1"}, presented)
	assert.Equal(t, 3, received)
	assert.Equal(t, 3, accepted)
}

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

// A 401 from the refresh endpoint means the refresh token is spent, expired or
// revoked. Presenting it again cannot work, and a spent one makes the server
// revoke the driver's other tokens, so the session ends instead.
func TestSessionEndsWhenTheServerRefusesTheRefresh(t *testing.T) {
	a := newAuthServer(t)
	s, _ := a.signIn()
	a.answerRefreshWith(http.StatusUnauthorized)
	a.advance(testTokenLifetime - refreshMargin)

	body := &closeRecorder{Reader: strings.NewReader(`{}`)}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, a.URL+"/api/v1/locations", body)
	require.NoError(t, err)
	_, err = sessionTransport{session: s, base: http.DefaultTransport}.RoundTrip(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signed out")
	assert.Contains(t, err.Error(), "401")
	assert.True(t, body.closed, "RoundTrip must close the request body when it fails")

	_, err = s.accessToken(t.Context())
	require.Error(t, err)
	presented, received, _ := a.counts()
	assert.Equal(t, []string{"r1"}, presented, "the refused refresh token was sent again")
	assert.Zero(t, received)
}

// The server takes a second use of a refresh token as theft and revokes every
// token the driver holds, so requests that find the token due together must
// share one refresh.
func TestSessionRefreshesOnceForConcurrentRequests(t *testing.T) {
	a := newAuthServer(t)
	_, client := a.signIn()
	a.advance(testTokenLifetime - refreshMargin)

	st := &stats{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			sendReport(t.Context(), client, a.URL, "sim-test-001", &locationReport{}, st)
		}()
	}
	close(start)
	wg.Wait()

	presented, _, accepted := a.counts()
	assert.Equal(t, []string{"r1"}, presented)
	assert.Equal(t, 8, accepted)
}

func TestCheckBaseURL(t *testing.T) {
	allowed := []string{
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"https://example.org",
		"https://example.org:8443/base",
	}
	for _, raw := range allowed {
		if err := checkBaseURL(raw); err != nil {
			t.Errorf("checkBaseURL(%q) = %v, want nil", raw, err)
		}
	}

	rejected := []string{
		"http://example.org",       // credentials in cleartext to a remote host
		"http://192.168.1.10:8080", // private, still not loopback
		"ftp://example.org",        // not an HTTP scheme
		"https://",                 // no host
		"://nonsense",              // unparseable
	}
	for _, raw := range rejected {
		if err := checkBaseURL(raw); err == nil {
			t.Errorf("checkBaseURL(%q) = nil, want an error", raw)
		}
	}
}

func TestReportBudgetWarning(t *testing.T) {
	// Each vehicle logs in as its own driver, so the budget is one report per
	// perDriverReportInterval per vehicle and the vehicle count does not enter
	// into it. Only an interval faster than that allowance warns.
	for _, vehicles := range []int{1, 2, 5, 20} {
		if got := reportBudgetWarning(vehicles, 3*time.Second); got == "" {
			t.Errorf("reportBudgetWarning(%d, 3s) = \"\", want a warning", vehicles)
		}
		for _, interval := range []time.Duration{5 * time.Second, 6 * time.Second, time.Minute} {
			if got := reportBudgetWarning(vehicles, interval); got != "" {
				t.Errorf("reportBudgetWarning(%d, %s) = %q, want \"\"", vehicles, interval, got)
			}
		}
	}
	// Adding vehicles used to shrink the per-vehicle budget. It must not now.
	if got := reportBudgetWarning(50, perDriverReportInterval); got != "" {
		t.Errorf("reportBudgetWarning(50, %s) = %q, want \"\"", perDriverReportInterval, got)
	}
	// Nonsense input must not warn rather than dividing by zero.
	if got := reportBudgetWarning(0, 0); got != "" {
		t.Errorf("reportBudgetWarning(0, 0) = %q, want \"\"", got)
	}
}

// TestProvisionDriversGivesEachVehicleItsOwnIdentity pins the reason this
// exists: the server rate-limits location reports per driver, so N vehicles
// need N distinct accounts and N distinct tokens.
func TestProvisionDriversGivesEachVehicleItsOwnIdentity(t *testing.T) {
	var mu sync.Mutex
	created := map[string]string{} // email -> password
	roles := map[string]string{}
	vehicles := map[string]bool{}
	assigned := map[float64]string{} // user id -> vehicle id

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding %s: %v", r.URL.Path, err)
		}
		str := func(k string) string { s, _ := body[k].(string); return s }
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/api/v1/auth/login" {
			if got := r.Header.Get("Authorization"); got != "Bearer admin-token" {
				t.Errorf("%s Authorization = %q, want the admin token", r.URL.Path, got)
			}
		}
		switch r.URL.Path {
		case "/api/v1/admin/vehicles":
			vehicles[str("id")] = true
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/admin/users":
			created[str("email")] = str("password")
			roles[str("email")] = str("role")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": len(created)})
		case "/api/v1/admin/assignments":
			id, _ := body["user_id"].(float64)
			if !vehicles[str("vehicle_id")] {
				t.Errorf("assigned to %s before it was created", str("vehicle_id"))
			}
			assigned[id] = str("vehicle_id")
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/auth/login":
			if created[str("email")] != str("password") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "token-for-" + str("email"), "refresh_token": "refresh-for-" + str("email"), "expires_in": 900,
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sessions, err := provisionDrivers(t.Context(), srv.Client(), srv.URL, "admin-token", "runid123", 3)
	if err != nil {
		t.Fatalf("provisionDrivers: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}
	seen := map[string]bool{}
	for _, s := range sessions {
		if seen[s.access] {
			t.Errorf("token %q reused; every vehicle needs its own rate-limit bucket", s.access)
		}
		seen[s.access] = true
	}
	if len(created) != 3 {
		t.Errorf("created %d accounts, want 3", len(created))
	}
	// The server only accepts reports from a driver assigned to the vehicle,
	// so every driver needs their own vehicle before starting a trip on it.
	if len(vehicles) != 3 || len(assigned) != 3 {
		t.Errorf("created %d vehicles and %d assignments, want 3 each", len(vehicles), len(assigned))
	}
	for i := range sessions {
		if id := simVehicleID("runid123", i); !vehicles[id] {
			t.Errorf("vehicle %s was not created", id)
		}
	}
	for userID, vehicleID := range assigned {
		if want := simVehicleID("runid123", int(userID)-1); vehicleID != want {
			t.Errorf("driver %v was assigned %q, want %q", userID, vehicleID, want)
		}
	}
	for email, role := range roles {
		if role != "driver" {
			t.Errorf("account %s has role %q, want driver", email, role)
		}
	}
	pw := map[string]bool{}
	for _, p := range created {
		if pw[p] {
			t.Error("two accounts share a password")
		}
		pw[p] = true
	}
}

// TestSameOriginOnlyBlocksCredentialLeaks pins why the policy exists:
// bearerTransport re-adds Authorization on every hop, so a redirect that
// leaves the validated origin would hand the token to another host.
func TestSameOriginOnlyBlocksCredentialLeaks(t *testing.T) {
	check := sameOriginOnly("http://localhost:8080")
	for _, tc := range []struct {
		to    string
		allow bool
	}{
		{"http://localhost:8080/api/v1/auth/login", true},
		{"http://localhost:80800/x", false},
		{"http://evil.example/x", false},
		{"https://localhost:8080/x", false}, // scheme change
		{"http://localhost:9090/x", false},  // port change
		{"http://127.0.0.1:8080/x", false},  // different host, same machine
	} {
		u, err := url.Parse(tc.to)
		if err != nil {
			t.Fatalf("parsing %s: %v", tc.to, err)
		}
		err = check(&http.Request{URL: u}, nil)
		if tc.allow && err != nil {
			t.Errorf("redirect to %s blocked: %v", tc.to, err)
		}
		if !tc.allow && err == nil {
			t.Errorf("redirect to %s allowed; it would leak credentials", tc.to)
		}
	}
	// Default ports are implicit but equal.
	if err := sameOriginOnly("http://example.com")(
		&http.Request{URL: mustParse(t, "http://example.com:80/x")}, nil); err != nil {
		t.Errorf("http://example.com:80 should equal http://example.com: %v", err)
	}
	// A live client must actually refuse the hop, not just the policy in isolation.
	var hit, stolen int
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		stolen++
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		http.Redirect(w, r, evil.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := &http.Client{
		Transport:     bearerTransport{token: "secret", base: http.DefaultTransport},
		CheckRedirect: sameOriginOnly(srv.URL),
	}
	resp, err := c.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Error("client followed a cross-origin redirect while carrying a bearer token")
	}
	if hit != 1 {
		t.Errorf("origin served %d requests, want 1", hit)
	}
	if stolen != 0 {
		t.Errorf("cross-origin server received %d requests, want 0", stolen)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %s: %v", raw, err)
	}
	return u
}

// TestFetchRouteIDs pins why it exists: the server rejects a route_id its
// GTFS schedule does not have, so reports must name routes it lists.
func TestFetchRouteIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/gtfs/routes" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"routes":[{"id":"R2","short_name":"2"},{"id":"R1","short_name":"1"}]}`))
	}))
	defer srv.Close()

	ids, err := fetchRouteIDs(t.Context(), srv.Client(), srv.URL, "tok")
	require.NoError(t, err)
	assert.Equal(t, []string{"R2", "R1"}, ids)

	_, err = fetchRouteIDs(t.Context(), srv.Client(), srv.URL, "wrong")
	assert.Error(t, err)
}
