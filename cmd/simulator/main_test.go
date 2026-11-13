package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestLoginReturnsAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/auth/login", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "sim@example.com", body["email"])
		assert.Equal(t, "hunter2", body["password"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-123"}`))
	}))
	defer server.Close()

	token, err := login(context.Background(), server.Client(), server.URL, "sim@example.com", "hunter2")
	require.NoError(t, err)
	assert.Equal(t, "tok-123", token)
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
	token, err := login(context.Background(), server.Client(), server.URL, "sim@example.com", "hunter2")
	require.Error(t, err)
	assert.Empty(t, token)
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
			json.NewEncoder(w).Encode(map[string]string{"access_token": "token-for-" + str("email")})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	tokens, err := provisionDrivers(t.Context(), srv.Client(), srv.URL, "admin-token", "runid123", 3)
	if err != nil {
		t.Fatalf("provisionDrivers: %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("got %d tokens, want 3", len(tokens))
	}
	seen := map[string]bool{}
	for _, tk := range tokens {
		if seen[tk] {
			t.Errorf("token %q reused; every vehicle needs its own rate-limit bucket", tk)
		}
		seen[tk] = true
	}
	if len(created) != 3 {
		t.Errorf("created %d accounts, want 3", len(created))
	}
	// The server only accepts reports from a driver assigned to the vehicle,
	// so every driver needs their own vehicle before starting a trip on it.
	if len(vehicles) != 3 || len(assigned) != 3 {
		t.Errorf("created %d vehicles and %d assignments, want 3 each", len(vehicles), len(assigned))
	}
	for i := range tokens {
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
