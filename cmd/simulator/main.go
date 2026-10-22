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
	RouteID   string  `json:"route_id"`
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
	adminToken, err := login(ctx, bootstrap, *baseURL, *email, *password)
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}

	runID, err := randomSecret()
	if err != nil {
		log.Fatal(err)
	}
	runID = runID[:8]

	scheduleRoutes, err := fetchRouteIDs(ctx, bootstrap, *baseURL, adminToken)
	if err != nil {
		log.Fatalf("listing GTFS routes: %v", err)
	}

	log.Printf("provisioning %d driver accounts", *numVehicles)
	drivers, err := provisionDrivers(ctx, bootstrap, *baseURL, adminToken, runID, *numVehicles, scheduleRoutes)
	if err != nil {
		endTrips(bootstrap, *baseURL, drivers)
		log.Fatalf("provisioning drivers: %v", err)
	}

	// One client per vehicle, each carrying its own driver's token. The tokens
	// are good for 24h and are never refreshed, so a -duration 0 run left going
	// for longer than that will start seeing 401s.
	clients := make([]*http.Client, len(drivers))
	for i, d := range drivers {
		clients[i] = &http.Client{
			Timeout:       10 * time.Second,
			Transport:     bearerTransport{token: d.token, base: http.DefaultTransport},
			CheckRedirect: sameOriginOnly(*baseURL),
		}
	}

	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	s := &stats{}

	log.Printf("starting simulator: %d vehicles, interval=%s, duration=%s", *numVehicles, *interval, *duration)

	var wg sync.WaitGroup
	for i := 0; i < *numVehicles; i++ {
		wg.Add(1)
		vehicleID, routeID := simVehicle(i, scheduleRoutes)
		route := routes[i%len(routes)]
		go func() {
			defer wg.Done()
			simulateVehicle(ctx, clients[i], *baseURL, vehicleID, route, routeID, *interval, s)
		}()
	}
	wg.Wait()
	endTrips(bootstrap, *baseURL, drivers)

	ok := s.succeeded.Load()
	fail := s.failed.Load()
	avgMS := int64(0)
	if ok > 0 {
		avgMS = s.totalMS.Load() / ok
	}
	log.Printf("simulation complete: %d requests, %d ok, %d failed, avg=%dms", ok+fail, ok, fail, avgMS)
}

func simulateVehicle(ctx context.Context, client *http.Client, baseURL, vehicleID string, route []Waypoint, routeID string, interval time.Duration, s *stats) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	waypointIdx := 0
	segmentStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			from := route[waypointIdx]
			to := route[(waypointIdx+1)%len(route)]

			segmentDist := haversineDistance(from, to)
			segmentDuration := segmentDist / 8.0 // assume ~8 m/s (~29 km/h, realistic urban bus)
			if segmentDuration <= 0 {
				segmentDuration = 1
			}

			elapsed := now.Sub(segmentStart).Seconds()
			t := elapsed / segmentDuration
			if t >= 1.0 {
				waypointIdx = (waypointIdx + 1) % len(route)
				segmentStart = now
				t = 0
				from = route[waypointIdx]
				to = route[(waypointIdx+1)%len(route)]
				segmentDist = haversineDistance(from, to)
				segmentDuration = segmentDist / 8.0
				if segmentDuration <= 0 {
					segmentDuration = 1
				}
			}

			pos := interpolate(from, to, t)
			brng := bearing(from, to)
			spd := speed(segmentDist, segmentDuration)

			report := locationReport{
				VehicleID: vehicleID,
				RouteID:   routeID,
				Latitude:  pos.Lat,
				Longitude: pos.Lon,
				Bearing:   brng,
				Speed:     spd,
				Accuracy:  5.0, // assume ~5m GPS accuracy for simulated reports
				Timestamp: now.Unix(),
			}

			sendReport(ctx, client, baseURL, vehicleID, &report, s)
		}
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

// createUserRequest is the POST /api/v1/admin/users body.
type createUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// simVehicle names the i-th (0-based) simulated vehicle and the route it
// drives, taken in turn from the schedule's route ids. The server refuses a
// trip on a route its GTFS schedule does not have, and then every report whose
// route is not the trip's, so the route is a real one; none at all when the
// schedule has no routes.
func simVehicle(i int, scheduleRoutes []string) (vehicleID, routeID string) {
	vehicleID = fmt.Sprintf("sim-vehicle-%03d", i+1)
	if len(scheduleRoutes) > 0 {
		routeID = scheduleRoutes[i%len(scheduleRoutes)]
	}
	return vehicleID, routeID
}

// simDriver is one provisioned driver: the token its vehicle reports with, and
// the trip it started, which endTrips ends when the run stops.
type simDriver struct {
	token  string
	tripID int64
}

// endTrips ends the trips a run started. Without it every run left its
// drivers' trips active on the server, and repeated runs piled them up. It has
// its own deadline, since the run's context is usually done by now (the
// -duration ran out, or Ctrl+C).
func endTrips(client *http.Client, baseURL string, drivers []simDriver) {
	if len(drivers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ended := 0
	for _, d := range drivers {
		if err := postJSON(ctx, client, baseURL+"/api/v1/trips/end", d.token, map[string]int64{"trip_id": d.tripID}, nil); err != nil {
			log.Printf("ending trip %d: %v", d.tripID, err)
			continue
		}
		ended++
	}
	log.Printf("ended %d of %d trips", ended, len(drivers))
}

// provisionDrivers creates one driver account per vehicle and logs each in, so
// every vehicle reports under its own JWT sub. The server rate-limits location
// reports per driver, so a shared login would put every vehicle in one bucket
// and turn most reports into 429s.
//
// The server accepts a report only from a driver assigned to an active
// vehicle with an active trip on it, so each driver also gets their vehicle,
// an assignment to it, and a started trip, the same way a driver app does.
//
// runID keeps the accounts of concurrent or repeated runs from colliding.
//
// On an error it returns the drivers provisioned so far with it, so their
// trips can still be ended.
func provisionDrivers(ctx context.Context, client *http.Client, baseURL, adminToken, runID string, n int, scheduleRoutes []string) ([]simDriver, error) {
	drivers := make([]simDriver, 0, n)
	for i := 0; i < n; i++ {
		vehicleID, routeID := simVehicle(i, scheduleRoutes)
		email := fmt.Sprintf("sim-driver-%03d-%s@simulator.invalid", i+1, runID)
		password, err := randomSecret()
		if err != nil {
			return drivers, err
		}
		if err := postJSON(ctx, client, baseURL+"/api/v1/admin/vehicles", adminToken,
			map[string]string{"id": vehicleID, "label": fmt.Sprintf("Simulated Vehicle %03d", i+1)}, nil); err != nil {
			return drivers, fmt.Errorf("creating vehicle %s: %w", vehicleID, err)
		}
		userID, err := createDriver(ctx, client, baseURL, adminToken, email, password, i+1)
		if err != nil {
			return drivers, err
		}
		if err := postJSON(ctx, client, baseURL+"/api/v1/admin/assignments", adminToken,
			map[string]any{"user_id": userID, "vehicle_id": vehicleID}, nil); err != nil {
			return drivers, fmt.Errorf("assigning driver %d to %s: %w", i+1, vehicleID, err)
		}
		token, err := loginWithRetry(ctx, client, baseURL, email, password)
		if err != nil {
			return drivers, fmt.Errorf("driver %d: %w", i+1, err)
		}
		var trip struct {
			ID int64 `json:"id"`
		}
		if err := postJSON(ctx, client, baseURL+"/api/v1/trips/start", token,
			map[string]string{"vehicle_id": vehicleID, "route_id": routeID}, &trip); err != nil {
			return drivers, fmt.Errorf("starting a trip for driver %d on %s: %w", i+1, vehicleID, err)
		}
		drivers = append(drivers, simDriver{token: token, tripID: trip.ID})
		if i < n-1 && !sleepCtx(ctx, provisionStagger) {
			return drivers, ctx.Err()
		}
	}
	return drivers, nil
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
func loginWithRetry(ctx context.Context, client *http.Client, baseURL, email, password string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= provisionAttempts; attempt++ {
		token, err := login(ctx, client, baseURL, email, password)
		if err == nil {
			return token, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "429") {
			return "", err
		}
		if attempt == provisionAttempts {
			break
		}
		log.Printf("login refused (429), waiting %s (attempt %d/%d)", provisionBackoff, attempt, provisionAttempts)
		if !sleepCtx(ctx, provisionBackoff) {
			return "", ctx.Err()
		}
	}
	return "", lastErr
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

// login exchanges credentials for a session token via POST /api/v1/auth/login.
func login(ctx context.Context, client *http.Client, baseURL, email, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("POST /api/v1/auth/login returned %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", fmt.Errorf("decoding login response: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("login response contained no token")
	}
	return out.Token, nil
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
