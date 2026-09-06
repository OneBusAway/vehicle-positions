package main

import (
	"bytes"
	"context"
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type locationReport struct {
	VehicleID string  `json:"vehicle_id"`
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

// checkBaseURL rejects a destination that would put the password and the
// session token on the wire in cleartext. Plain HTTP stays allowed for
// loopback, which is the default and the only way the simulator is normally
// run, but anything remote has to be HTTPS.
// perDriverReportInterval mirrors rateInterval in ratelimit.go: the server
// allows one location report per driver per this long, keyed on the JWT sub.
const perDriverReportInterval = 5 * time.Second

// reportBudgetWarning explains the shortfall when the requested rate exceeds
// what one login is allowed, which is every run with more than one vehicle
// because all of them authenticate as the same user.
func reportBudgetWarning(vehicles int, interval time.Duration) string {
	if vehicles <= 0 || interval <= 0 {
		return ""
	}
	needed := time.Duration(vehicles) * perDriverReportInterval
	if interval >= needed {
		return ""
	}
	return fmt.Sprintf(
		"%d vehicles every %s is %s per report, but the server allows one per %s per driver "+
			"and every vehicle here logs in as the same user, so most reports will come back 429. "+
			"Use -vehicles 1, or -interval %s, until the simulator can hold one account per vehicle.",
		vehicles, interval, interval/time.Duration(vehicles), perDriverReportInterval, needed)
}

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
	email := flag.String("email", os.Getenv("ADMIN_BOOTSTRAP_EMAIL"), "Account email for login (default $ADMIN_BOOTSTRAP_EMAIL)")
	password := flag.String("password", os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"), "Account password for login (default $ADMIN_BOOTSTRAP_PASSWORD)")
	flag.Parse()

	if *numVehicles <= 0 {
		log.Fatal("vehicles must be positive")
	}
	if *interval <= 0 {
		log.Fatal("interval must be positive")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

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

	token, err := login(ctx, &http.Client{Timeout: 10 * time.Second}, *baseURL, *email, *password)
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: bearerTransport{token: token, base: http.DefaultTransport},
	}
	s := &stats{}

	log.Printf("starting simulator: %d vehicles, interval=%s, duration=%s", *numVehicles, *interval, *duration)

	var wg sync.WaitGroup
	for i := 0; i < *numVehicles; i++ {
		wg.Add(1)
		vehicleID := fmt.Sprintf("sim-vehicle-%03d", i+1)
		route := routes[i%len(routes)]
		go func() {
			defer wg.Done()
			simulateVehicle(ctx, client, *baseURL, vehicleID, route, *interval, s)
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
}

func simulateVehicle(ctx context.Context, client *http.Client, baseURL, vehicleID string, route []Waypoint, interval time.Duration, s *stats) {
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
