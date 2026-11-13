package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type locationReport struct {
	VehicleID string  `json:"vehicle_id"`
	TripID    string  `json:"trip_id"`
	RouteID   string  `json:"route_id"`
	StartDate string  `json:"start_date"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Bearing   float64 `json:"bearing"`
	Speed     float64 `json:"speed"`
	Accuracy  float64 `json:"accuracy"`
	Timestamp int64   `json:"timestamp"`
}

type stats struct {
	succeeded atomic.Int64
	failed    atomic.Int64
	totalMS   atomic.Int64
}

// perDriverReportInterval mirrors rateInterval in ratelimit.go: the server
// allows one location report per driver per this long, keyed on the JWT sub.
const perDriverReportInterval = 5 * time.Second

// provisionStagger spaces the driver logins out and provisionBackoff is how
// long a refused one waits: the server allows ten logins per IP per minute, so
// a refusal has to sit out most of that window.
const (
	provisionStagger  = 250 * time.Millisecond
	provisionBackoff  = 12 * time.Second
	provisionAttempts = 10
)

// refreshMargin is how long before its access token expires a vehicle
// refreshes it, as the driver apps do. When a refresh fails for a reason that
// may pass, such as a 429 or a dropped connection, the vehicle's requests in
// the rest of that minute try again while the old token still works.
const refreshMargin = time.Minute

// reportBudgetWarning explains the shortfall when a single vehicle reports
// faster than the server's per-driver allowance. Each vehicle authenticates as
// its own driver, so the budget is per vehicle and does not shrink as more are
// added.
func reportBudgetWarning(vehicles int, interval time.Duration) string {
	if vehicles <= 0 || interval <= 0 || interval >= perDriverReportInterval {
		return ""
	}
	return fmt.Sprintf(
		"-interval %s is faster than the one report per %s the server allows each driver, "+
			"so most reports will come back 429. Use -interval %s or slower.",
		interval, perDriverReportInterval, perDriverReportInterval)
}

// checkBaseURL rejects a destination that would put the password and the
// session token on the wire in cleartext. Plain HTTP stays allowed for
// loopback, which is the default and the only way the simulator is normally
// run, but anything remote has to be HTTPS.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid -url %q: %w", raw, err)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid -url %q: no host", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("-url %q sends the login password and the bearer token in cleartext; use https for a remote host", raw)
	default:
		return fmt.Errorf("invalid -url %q: scheme must be http or https", raw)
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Server base URL")
	numVehicles := flag.Int("vehicles", 10, "Number of simulated vehicles")
	interval := flag.Duration("interval", 10*time.Second, "Time between location reports per vehicle")
	duration := flag.Duration("duration", 5*time.Minute, "Total simulation duration (0 = run until Ctrl+C)")
	email := flag.String("email", "", "Account email for login (default $ADMIN_BOOTSTRAP_EMAIL)")
	password := flag.String("password", "", "Account password for login (default $ADMIN_BOOTSTRAP_PASSWORD)")
	flag.Parse()
	if *email == "" {
		*email = os.Getenv("ADMIN_BOOTSTRAP_EMAIL")
	}
	if *password == "" {
		*password = os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")
	}

	if *numVehicles <= 0 {
		log.Fatal("vehicles must be positive")
	}
	if *interval <= 0 {
		log.Fatal("interval must be positive")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := checkBaseURL(*baseURL); err != nil {
		log.Fatal(err)
	}

	if warning := reportBudgetWarning(*numVehicles, *interval); warning != "" {
		log.Printf("warning: %s", warning)
	}

	if *email == "" || *password == "" {
		log.Fatal("email and password are required: POST /api/v1/locations is authenticated, " +
			"so pass -email/-password or set ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD")
	}

	bootstrap := &http.Client{Timeout: 10 * time.Second, CheckRedirect: sameOriginOnly(*baseURL)}
	admin, err := login(ctx, bootstrap, *baseURL, *email, *password)
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}
	adminToken := admin.AccessToken

	runID, err := randomSecret()
	if err != nil {
		log.Fatal(err)
	}
	runID = runID[:8]

	scheduleRoutes, err := fetchRouteIDs(ctx, bootstrap, *baseURL, adminToken)
	if err != nil {
		log.Fatalf("listing GTFS routes: %v", err)
	}
	if len(scheduleRoutes) == 0 {
		log.Fatal("the server's GTFS schedule lists no routes, so there is no trip to drive a vehicle along")
	}

	log.Printf("provisioning %d driver accounts", *numVehicles)
	sessions, err := provisionDrivers(ctx, bootstrap, *baseURL, adminToken, runID, *numVehicles)
	if err != nil {
		log.Fatalf("provisioning drivers: %v", err)
	}

	// One client per vehicle, each carrying its own driver's access token and
	// refreshing it before it expires, so a run can outlast ACCESS_TOKEN_TTL.
	clients := make([]*http.Client, len(sessions))
	for i, s := range sessions {
		clients[i] = &http.Client{
			Timeout:       10 * time.Second,
			Transport:     sessionTransport{session: s, base: http.DefaultTransport},
			CheckRedirect: sameOriginOnly(*baseURL),
		}
	}

	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	s := &stats{}

	log.Printf("starting simulator: %d vehicles, interval=%s, duration=%s", *numVehicles, *interval, *duration)

	claims := newTripClaims()
	var stopped atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < *numVehicles; i++ {
		wg.Add(1)
		// Routes are taken in turn, so the vehicles are spread over the schedule.
		// token is the access token the driver signed in with. Starting and
		// ending a trip pass it to postJSON, but the client's transport
		// replaces that header with the session's current token.
		v := &vehicle{
			id: simVehicleID(runID, i), routeID: scheduleRoutes[i%len(scheduleRoutes)],
			baseURL: *baseURL, token: sessions[i].access, client: clients[i],
			interval: *interval, claims: claims, stats: s,
		}
		go func() {
			defer wg.Done()
			if err := v.drive(ctx); err != nil {
				log.Printf("%s: stopped: %v", v.id, err)
				stopped.Add(1)
			}
		}()
	}
	wg.Wait()

	ok := s.succeeded.Load()
	fail := s.failed.Load()
	avgMS := int64(0)
	if ok > 0 {
		avgMS = s.totalMS.Load() / ok
	}
	log.Printf("simulation complete: %d requests, %d ok, %d failed, avg=%dms", ok+fail, ok, fail, avgMS)
	if n := stopped.Load(); n > 0 {
		log.Fatalf("%d of %d vehicles stopped before the run ended", n, *numVehicles)
	}
}

// bearerTransport attaches the session token to every simulator request.
// Reports go to POST /api/v1/locations, which sits behind requireAuth, so an
// unauthenticated run fails with 401 on every single report.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

// sessionTransport sends each of a vehicle's requests with its driver's
// current access token, which the session refreshes before it expires.
type sessionTransport struct {
	session *session
	base    http.RoundTripper
}

func (t sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.session.accessToken(req.Context())
	if err != nil {
		// A RoundTripper must close the request body even when it fails.
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	return bearerTransport{token: token, base: t.base}.RoundTrip(req)
}

// session is one driver's sign-in. Like the driver apps, it refreshes the
// access token refreshMargin before the token expires, so the vehicle keeps
// reporting for as long as the run lasts.
type session struct {
	vehicleID string
	baseURL   string
	// client sends the refresh. It must not be a client whose transport is
	// this session's sessionTransport, or the refresh would wait on itself.
	client *http.Client
	now    func() time.Time

	// mu makes the refresh single-flight. The server takes a second use of a
	// refresh token as theft and revokes every token the driver holds.
	mu        sync.Mutex
	access    string
	refresh   string
	expiresAt time.Time
	// refused is set when the server answers a refresh with 401. The refresh
	// token can never work again, so the session ends rather than sending it
	// on every request.
	refused error
}

func newSession(vehicleID, baseURL string, client *http.Client, tokens tokenResponse, now func() time.Time) *session {
	s := &session{vehicleID: vehicleID, baseURL: baseURL, client: client, now: now}
	s.store(tokens, now())
	return s
}

// store keeps a token pair issued at about issued. expires_in is relative, so
// the expiry is counted on the simulator's own clock and skew between it and
// the server's does not matter.
func (s *session) store(tokens tokenResponse, issued time.Time) {
	s.access = tokens.AccessToken
	s.refresh = tokens.RefreshToken
	s.expiresAt = issued.Add(time.Duration(tokens.ExpiresIn) * time.Second)
}

// accessToken returns the token to send, refreshing it first once it is
// within refreshMargin of expiring. If the refresh fails for a reason that may
// pass, a token that has not expired is still returned, and the next request
// tries the refresh again.
func (s *session) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refused != nil {
		return "", s.refused
	}
	now := s.now()
	if now.Before(s.expiresAt.Add(-refreshMargin)) {
		return s.access, nil
	}
	tokens, err := postForTokens(ctx, s.client, s.baseURL+"/api/v1/auth/refresh",
		map[string]string{"refresh_token": s.refresh})
	if err == nil {
		s.store(tokens, now)
		return s.access, nil
	}
	var status *statusError
	if errors.As(err, &status) && status.code == http.StatusUnauthorized {
		s.refused = fmt.Errorf("the driver is signed out: %w", err)
		return "", s.refused
	}
	if now.Before(s.expiresAt) {
		log.Printf("%s: refreshing the access token failed, will try again on the next request: %v", s.vehicleID, err)
		return s.access, nil
	}
	return "", fmt.Errorf("refreshing the expired access token: %w", err)
}

// createUserRequest is the POST /api/v1/admin/users body.
type createUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// simVehicleID names the i-th (0-based) simulated vehicle of a run. The run id
// is in it because a vehicle has one active trip at a time, and the vehicles of
// a concurrent run are busy with theirs.
func simVehicleID(runID string, i int) string {
	return fmt.Sprintf("sim-%s-%03d", runID, i+1)
}

// provisionDrivers creates one driver account per vehicle and logs each in, so
// every vehicle reports under its own JWT sub. The server rate-limits location
// reports per driver, so a shared login would put every vehicle in one bucket
// and turn most reports into 429s.
//
// The server accepts a report only from a driver assigned to an active
// vehicle, so each driver also gets their vehicle and an assignment to it.
// The trip a report needs as well is started by the vehicle as it drives.
//
// runID keeps the accounts of concurrent or repeated runs from colliding.
//
// It returns each driver's session, in vehicle order. The sessions refresh
// through client, so it must not carry a driver's token itself.
func provisionDrivers(ctx context.Context, client *http.Client, baseURL, adminToken, runID string, n int) ([]*session, error) {
	sessions := make([]*session, 0, n)
	for i := 0; i < n; i++ {
		vehicleID := simVehicleID(runID, i)
		email := fmt.Sprintf("sim-driver-%03d-%s@simulator.invalid", i+1, runID)
		password, err := randomSecret()
		if err != nil {
			return nil, err
		}
		if err := postJSON(ctx, client, baseURL+"/api/v1/admin/vehicles", adminToken,
			map[string]string{"id": vehicleID, "label": fmt.Sprintf("Simulated Vehicle %03d", i+1)}, nil); err != nil {
			return nil, fmt.Errorf("creating vehicle %s: %w", vehicleID, err)
		}
		userID, err := createDriver(ctx, client, baseURL, adminToken, email, password, i+1)
		if err != nil {
			return nil, err
		}
		if err := postJSON(ctx, client, baseURL+"/api/v1/admin/assignments", adminToken,
			map[string]any{"user_id": userID, "vehicle_id": vehicleID}, nil); err != nil {
			return nil, fmt.Errorf("assigning driver %d to %s: %w", i+1, vehicleID, err)
		}
		tokens, err := loginWithRetry(ctx, client, baseURL, email, password)
		if err != nil {
			return nil, fmt.Errorf("driver %d: %w", i+1, err)
		}
		sessions = append(sessions, newSession(vehicleID, baseURL, client, tokens, time.Now))
		if i < n-1 && !sleepCtx(ctx, provisionStagger) {
			return nil, ctx.Err()
		}
	}
	return sessions, nil
}

// createDriver creates a driver account and returns its user id.
func createDriver(ctx context.Context, client *http.Client, baseURL, adminToken, email, password string, n int) (int64, error) {
	var user struct {
		ID int64 `json:"id"`
	}
	err := postJSON(ctx, client, baseURL+"/api/v1/admin/users", adminToken, createUserRequest{
		Name:     fmt.Sprintf("Simulated Driver %03d", n),
		Email:    email,
		Password: password,
		Role:     "driver",
	}, &user)
	if err != nil {
		return 0, fmt.Errorf("creating driver %d: %w. "+
			"The account this simulator logs in with must have the admin role", n, err)
	}
	return user.ID, nil
}

// postJSON POSTs body as JSON with the bearer token and, when out is non-nil,
// decodes the response into it. Any status other than 200 or 201 is an error
// carrying the server's message.
func postJSON(ctx context.Context, client *http.Client, url, token string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("POST %s returned %d: %s", req.URL.Path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding POST %s response: %w", req.URL.Path, err)
		}
	}
	return nil
}

// loginWithRetry backs off past a 429 from the per-IP login limiter, which one
// login per vehicle will reach on a run with more than ten of them.
func loginWithRetry(ctx context.Context, client *http.Client, baseURL, email, password string) (tokenResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= provisionAttempts; attempt++ {
		tokens, err := login(ctx, client, baseURL, email, password)
		if err == nil {
			return tokens, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "429") {
			return tokenResponse{}, err
		}
		if attempt == provisionAttempts {
			break
		}
		log.Printf("login refused (429), waiting %s (attempt %d/%d)", provisionBackoff, attempt, provisionAttempts)
		if !sleepCtx(ctx, provisionBackoff) {
			return tokenResponse{}, ctx.Err()
		}
	}
	return tokenResponse{}, lastErr
}

// randomSecret returns a password for an account that only this run uses.
func randomSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a driver password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// sleepCtx reports whether it slept the whole duration rather than being cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// canonicalOrigin is scheme://host:port with the scheme's default port made
// explicit, so http://h and http://h:80 compare equal.
func canonicalOrigin(u *url.URL) string {
	host, port := u.Hostname(), u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port)
}

// sameOriginOnly refuses a redirect that leaves the origin checkBaseURL
// validated. http.Client normally strips Authorization when a redirect crosses
// origins, but bearerTransport sets the header inside RoundTrip, which runs
// again for every hop and puts it back. Without this a redirect could hand a
// driver's token, or the admin password on the login hop, to another host.
func sameOriginOnly(base string) func(*http.Request, []*http.Request) error {
	u, err := url.Parse(base)
	if err != nil {
		return func(*http.Request, []*http.Request) error { return err }
	}
	want := canonicalOrigin(u)
	return func(req *http.Request, via []*http.Request) error {
		if got := canonicalOrigin(req.URL); got != want {
			return fmt.Errorf("refusing redirect to %s: it would send credentials to an origin other than %s", got, want)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

// login exchanges credentials for tokens via POST /api/v1/auth/login.
func login(ctx context.Context, client *http.Client, baseURL, email, password string) (tokenResponse, error) {
	return postForTokens(ctx, client, baseURL+"/api/v1/auth/login",
		map[string]string{"email": email, "password": password})
}

// tokenResponse is the part of a login or refresh response the simulator
// uses; both endpoints answer with the same shape.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// statusError is an answer other than 200 from the login or refresh endpoint.
type statusError struct {
	path string
	code int
	body []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("POST %s returned %d: %s", e.path, e.code, e.body)
}

// postForTokens POSTs body to the login or the refresh endpoint and returns
// the tokens it answers with. A vehicle needs all three fields to keep
// reporting past its first access token, so a response missing one is an
// error.
func postForTokens(ctx context.Context, client *http.Client, endpoint string, body map[string]string) (tokenResponse, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return tokenResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return tokenResponse{}, &statusError{path: req.URL.Path, code: resp.StatusCode, body: bytes.TrimSpace(msg)}
	}

	var out tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return tokenResponse{}, fmt.Errorf("decoding POST %s response: %w", req.URL.Path, err)
	}
	switch {
	case out.AccessToken == "":
		return tokenResponse{}, fmt.Errorf("POST %s response contained no access_token", req.URL.Path)
	case out.RefreshToken == "":
		return tokenResponse{}, fmt.Errorf("POST %s response contained no refresh_token", req.URL.Path)
	case out.ExpiresIn <= 0:
		return tokenResponse{}, fmt.Errorf("POST %s response contained no positive expires_in", req.URL.Path)
	}
	return out, nil
}

// fetchRouteIDs lists the route ids in the server's GTFS schedule.
func fetchRouteIDs(ctx context.Context, client *http.Client, baseURL, token string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/gtfs/routes", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET /api/v1/gtfs/routes returned %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	var out struct {
		Routes []struct {
			ID string `json:"id"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding routes: %w", err)
	}
	ids := make([]string, len(out.Routes))
	for i, r := range out.Routes {
		ids[i] = r.ID
	}
	return ids, nil
}

func sendReport(ctx context.Context, client *http.Client, baseURL, vehicleID string, report *locationReport, s *stats) {
	body, err := json.Marshal(report)
	if err != nil {
		log.Printf("%s: marshal error: %v", vehicleID, err)
		s.failed.Add(1)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/locations", bytes.NewReader(body))
	if err != nil {
		log.Printf("%s: request error: %v", vehicleID, err)
		s.failed.Add(1)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return // clean shutdown, not a real failure
		}
		log.Printf("%s: POST failed: %v", vehicleID, err)
		s.failed.Add(1)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		io.Copy(io.Discard, resp.Body)
		s.succeeded.Add(1)
		s.totalMS.Add(latency.Milliseconds())
		log.Printf("%s: POST %d (%dms)", vehicleID, resp.StatusCode, latency.Milliseconds())
	} else {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		s.failed.Add(1)
		log.Printf("%s: POST %d (%dms): %s", vehicleID, resp.StatusCode, latency.Milliseconds(), string(bodyBytes))
	}
}
