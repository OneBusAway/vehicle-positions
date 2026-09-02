// Command ridersim drives the rider-mode API the way a phone would: it walks
// one or more scheduled trips along their GTFS shapes in real time, uploading
// batches of positions and printing what the server makes of them.
//
// It talks to the server over HTTP only, so the wire types below are its own
// copies of the API's request and response bodies rather than imports.
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
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	// offRouteMetres is how far east of the shape an -offroute-after rider
	// steps: far enough to be off route whatever the accuracy allowance.
	offRouteMetres = 300
	// reportedAccuracy is the horizontal accuracy every simulated fix claims,
	// comfortably inside the server's 100 m ceiling.
	reportedAccuracy = 5
	// pickSeed keeps -random reproducible from run to run.
	pickSeed = 1
	// httpTimeout bounds one API call.
	httpTimeout = 10 * time.Second
	// metresPerDegree is the local equirectangular scale factor, matching the
	// one the rider package uses.
	metresPerDegree = 111_320.0
)

// config is the simulation as the flags describe it. It is shared, read-only,
// by every rider goroutine.
type config struct {
	api           *apiClient
	index         *rider.Index
	startDate     string
	interval      time.Duration
	speed         float64
	noise         float64
	offRouteAfter time.Duration
	duration      time.Duration
}

// --- wire types (spec §4.4) ------------------------------------------------

type registerRequest struct {
	InstallationID string `json:"installation_id"`
	Platform       string `json:"platform"`
	AppID          string `json:"app_id"`
	AppVersion     string `json:"app_version"`
}

type registerResponse struct {
	RiderID               string `json:"rider_id"`
	Token                 string `json:"token"`
	ReportIntervalSeconds int    `json:"report_interval_seconds"`
	MaxBatchSize          int    `json:"max_batch_size"`
}

type startRideRequest struct {
	TripID    string `json:"trip_id"`
	StartDate string `json:"start_date"`
}

type startRideResponse struct {
	RideID                string `json:"ride_id"`
	State                 string `json:"state"`
	ReportIntervalSeconds int    `json:"report_interval_seconds"`
	MaxBatchSize          int    `json:"max_batch_size"`
}

type position struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Accuracy  float64 `json:"accuracy"`
	Speed     float64 `json:"speed"`
	Bearing   float64 `json:"bearing"`
	Timestamp int64   `json:"timestamp"`
}

type positionsRequest struct {
	Positions []position `json:"positions"`
}

type positionsResponse struct {
	State          string `json:"state"`
	Published      bool   `json:"published"`
	Corroboration  string `json:"corroboration"`
	Accepted       int    `json:"accepted"`
	Ignored        int    `json:"ignored"`
	OffRouteStreak int    `json:"off_route_streak"`
	Ended          bool   `json:"ended"`
	EndReason      string `json:"end_reason"`
}

type endRideRequest struct {
	Reason string `json:"reason"`
}

// --- HTTP ------------------------------------------------------------------

// apiClient posts JSON to the rider API. It reports the status code rather
// than turning every non-2xx into an error, because 409 (the ride is gone) and
// 429 (too fast) are part of the protocol a client has to handle.
type apiClient struct {
	base string
	http *http.Client
}

func (a *apiClient) post(ctx context.Context, path, token string, in any) (int, []byte, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return 0, nil, fmt.Errorf("encode %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return resp.StatusCode, data, nil
}

// postJSON is post plus the common case: one expected status, decoded into out.
func (a *apiClient) postJSON(ctx context.Context, path, token string, in, out any, want ...int) error {
	status, body, err := a.post(ctx, path, token, in)
	if err != nil {
		return err
	}
	if !slices.Contains(want, status) {
		return fmt.Errorf("POST %s: %d: %s", path, status, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// --- pure helpers ----------------------------------------------------------

// jitter displaces pos by an independent Gaussian of metres standard deviation
// in each of latitude and longitude, standing in for GPS error.
func jitter(pos rider.LatLon, metres float64, rng *rand.Rand) rider.LatLon {
	if metres <= 0 {
		return pos
	}
	cosLat := math.Cos(pos.Lat * math.Pi / 180)
	if cosLat < 1e-6 {
		cosLat = 1e-6
	}
	return rider.LatLon{
		Lat: pos.Lat + rng.NormFloat64()*metres/metresPerDegree,
		Lon: pos.Lon + rng.NormFloat64()*metres/(metresPerDegree*cosLat),
	}
}

// offsetEast moves pos the given number of metres due east, which is how the
// simulator leaves the shape on purpose.
func offsetEast(pos rider.LatLon, metres float64) rider.LatLon {
	cosLat := math.Cos(pos.Lat * math.Pi / 180)
	if cosLat < 1e-6 {
		cosLat = 1e-6
	}
	return rider.LatLon{Lat: pos.Lat, Lon: pos.Lon + metres/(metresPerDegree*cosLat)}
}

// pickTrips resolves the trips to simulate: every explicitly requested id
// (which must be in the feed and running on serviceDate), plus random more
// drawn from the trips active that day. The draw is seeded so repeated runs
// exercise the same trips.
func pickTrips(ix *rider.Index, requested []string, random int, serviceDate string) ([]string, error) {
	trips := make([]string, 0, len(requested)+max(random, 0))
	for _, id := range requested {
		trip, ok := ix.Trip(id)
		if !ok {
			return nil, fmt.Errorf("trip %q is not in the feed", id)
		}
		if !ix.ActiveOn(trip, serviceDate) {
			return nil, fmt.Errorf("trip %q does not run on %s", id, serviceDate)
		}
		trips = append(trips, id)
	}

	if random <= 0 {
		if len(trips) == 0 {
			return nil, errors.New("nothing to simulate: pass -trip or -random")
		}
		return trips, nil
	}

	var active []string
	for _, id := range ix.TripIDs() {
		trip, ok := ix.Trip(id)
		if ok && ix.ActiveOn(trip, serviceDate) {
			active = append(active, id)
		}
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("no trips run on %s", serviceDate)
	}
	if random > len(active) {
		return nil, fmt.Errorf("-random %d: only %d trips run on %s", random, len(active), serviceDate)
	}
	rng := rand.New(rand.NewSource(pickSeed))
	rng.Shuffle(len(active), func(i, j int) { active[i], active[j] = active[j], active[i] })
	return append(trips, active[:random]...), nil
}

// --- one rider -------------------------------------------------------------

// runRider registers a fresh rider, opens a ride on tripID and walks the trip's
// shape until it runs out, the ride ends, or the deadline passes. It returns
// the reason the ride ended.
func runRider(ctx context.Context, cfg *config, tripID string, rng *rand.Rand) (string, error) {
	trip, ok := cfg.index.Trip(tripID)
	if !ok {
		return "", fmt.Errorf("trip %q is not in the feed", tripID)
	}

	var reg registerResponse
	err := cfg.api.postJSON(ctx, "/api/v1/rider/register", "", registerRequest{
		InstallationID: uuid.NewString(),
		Platform:       "other",
		AppID:          "org.onebusaway.ridersim",
		AppVersion:     "1.0",
	}, &reg, http.StatusCreated, http.StatusOK)
	if err != nil {
		return "", err
	}

	var ride startRideResponse
	err = cfg.api.postJSON(ctx, "/api/v1/rider/rides", reg.Token, startRideRequest{
		TripID:    tripID,
		StartDate: cfg.startDate,
	}, &ride, http.StatusCreated)
	if err != nil {
		return "", err
	}

	report := time.Duration(ride.ReportIntervalSeconds) * time.Second
	if report <= 0 {
		report = 5 * time.Second
	}
	maxBatch := ride.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = 1
	}
	tag := fmt.Sprintf("%s/%.8s", tripID, ride.RideID)
	log.Printf("%s: ride started state=%s report=%s batch=%d shape=%.0fm", tag, ride.State, report, maxBatch, trip.Shape.Length)

	r := &riderRun{
		cfg: cfg, trip: trip, tag: tag, token: reg.Token, rideID: ride.RideID,
		report: report, maxBatch: maxBatch, rng: rng, state: ride.State,
	}
	return r.walk(ctx)
}

// riderRun is one ride in progress.
type riderRun struct {
	cfg      *config
	trip     *rider.TripInfo
	tag      string
	token    string
	rideID   string
	report   time.Duration
	maxBatch int
	rng      *rand.Rand

	along   float64
	buf     []position
	sent    int
	dropped int

	state         string
	corroboration string
	published     bool
}

// walk samples the shape on one ticker and uploads on another until the ride is
// over, then ends it.
func (r *riderRun) walk(ctx context.Context) (string, error) {
	started := time.Now()
	sample := time.NewTicker(r.cfg.interval)
	defer sample.Stop()
	upload := time.NewTicker(r.report)
	defer upload.Stop()

	// A zero -duration means "until the shape runs out"; a timer that never
	// fires keeps the select below uniform.
	deadline := time.NewTimer(time.Duration(math.MaxInt64))
	if r.cfg.duration > 0 {
		deadline.Reset(r.cfg.duration)
	}
	defer deadline.Stop()

	for {
		var clientReason rider.EndReason
		select {
		case <-ctx.Done():
			clientReason = rider.EndUserRequested
		case <-deadline.C:
			clientReason = rider.EndMaxDuration
		case <-sample.C:
			if !r.advance(started) {
				continue
			}
			clientReason = rider.EndArrived
		case <-upload.C:
			reason, err := r.flush(ctx)
			if err != nil || reason != "" {
				return reason, err
			}
			continue
		}

		// The ride is over one way or another: send whatever is buffered, so
		// the server sees the last of the trip, then end it. A server-side
		// ending during that final batch wins — it is the real reason.
		if ctx.Err() == nil {
			reason, err := r.flush(ctx)
			if err != nil {
				return "", err
			}
			if reason != "" {
				return reason, nil
			}
		}
		return r.end(clientReason)
	}
}

// advance moves the rider along the shape by one interval and buffers the fix.
// It reports whether the shape has run out.
func (r *riderRun) advance(started time.Time) bool {
	r.along += r.cfg.speed * r.cfg.interval.Seconds()
	arrived := r.along >= r.trip.Shape.Length
	if arrived {
		r.along = r.trip.Shape.Length
	}

	pos := jitter(r.trip.Shape.PointAt(r.along), r.cfg.noise, r.rng)
	if r.cfg.offRouteAfter > 0 && time.Since(started) >= r.cfg.offRouteAfter {
		pos = offsetEast(pos, offRouteMetres)
	}
	r.buf = append(r.buf, position{
		Latitude:  pos.Lat,
		Longitude: pos.Lon,
		Accuracy:  reportedAccuracy,
		Speed:     r.cfg.speed,
		Bearing:   r.trip.Shape.BearingAt(r.along),
		Timestamp: time.Now().Unix(),
	})
	return arrived
}

// flush uploads the buffered fixes, at most maxBatch of them. It returns the
// end reason if the server ended the ride, and empty otherwise.
func (r *riderRun) flush(ctx context.Context) (string, error) {
	if len(r.buf) == 0 {
		return "", nil
	}
	// More fixes than one batch can carry means the sampling interval outruns
	// the reporting interval; the newest are the ones worth sending.
	if len(r.buf) > r.maxBatch {
		r.dropped += len(r.buf) - r.maxBatch
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-r.maxBatch:]...)
	}

	status, body, err := r.cfg.api.post(ctx, "/api/v1/rider/rides/"+r.rideID+"/positions", r.token, positionsRequest{Positions: r.buf})
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		// Reporting faster than the server allows: keep the buffer and try on
		// the next tick.
		log.Printf("%s: rate limited, retrying next interval", r.tag)
		return "", nil
	case http.StatusConflict:
		// The server has forgotten the ride (restart, or reaped); there is no
		// reason to be had from it any more.
		log.Printf("%s: ride is gone (409)", r.tag)
		r.buf = r.buf[:0]
		return "gone", nil
	default:
		return "", fmt.Errorf("POST positions: %d: %s", status, bytes.TrimSpace(body))
	}

	var res positionsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("decode positions response: %w", err)
	}
	r.sent += len(r.buf)
	r.buf = r.buf[:0]

	if res.State != r.state || res.Corroboration != r.corroboration || res.Published != r.published {
		log.Printf("%s: state=%s corroboration=%s published=%t accepted=%d ignored=%d off_route_streak=%d",
			r.tag, res.State, res.Corroboration, res.Published, res.Accepted, res.Ignored, res.OffRouteStreak)
		r.state, r.corroboration, r.published = res.State, res.Corroboration, res.Published
	}
	if res.Ended {
		log.Printf("%s: server ended the ride: %s", r.tag, res.EndReason)
		return res.EndReason, nil
	}
	return "", nil
}

// end closes the ride from the client's side. A ride the server has already
// forgotten is not an error: it ended, and the reason is the one asked for.
func (r *riderRun) end(reason rider.EndReason) (string, error) {
	// The context is gone on an interrupt, so the final call gets its own.
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	err := r.cfg.api.postJSON(ctx, "/api/v1/rider/rides/"+r.rideID+"/end", r.token,
		endRideRequest{Reason: string(reason)}, nil, http.StatusOK, http.StatusConflict)
	if err != nil {
		return "", err
	}
	log.Printf("%s: ended %s after %.0fm along the shape (%d points sent, %d dropped)",
		r.tag, reason, r.along, r.sent, r.dropped)
	return string(reason), nil
}

// --- main ------------------------------------------------------------------

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Server base URL")
	gtfsSource := flag.String("gtfs", "rider/testdata/fixture.zip", "GTFS static zip path or URL")
	random := flag.Int("random", 0, "Number of extra trips to pick at random from the ones running that day")
	startDate := flag.String("start-date", "", "Service date as YYYYMMDD (default: today in the feed's timezone)")
	interval := flag.Duration("interval", 5*time.Second, "Time between simulated GPS fixes")
	speed := flag.Float64("speed", 10, "Metres per second along the shape")
	noise := flag.Float64("noise", 8, "Metres of Gaussian jitter added to each fix")
	offRouteAfter := flag.Duration("offroute-after", 0, "Leave the shape by 300 m after this long (0 = stay on it)")
	ridersPerTrip := flag.Int("riders-per-trip", 1, "Riders to simulate on each trip")
	duration := flag.Duration("duration", 0, "Stop each ride after this long (0 = when the shape runs out)")
	expectEnd := flag.String("expect-end", "", "Require every ride to end with this reason, e.g. arrived or off_route")

	var requested []string
	flag.Func("trip", "Trip id to simulate (repeatable)", func(v string) error {
		if v == "" {
			return errors.New("trip id must not be empty")
		}
		requested = append(requested, v)
		return nil
	})
	flag.Parse()

	if err := run(*baseURL, *gtfsSource, *startDate, *expectEnd, requested, *random, *ridersPerTrip, &config{
		interval:      *interval,
		speed:         *speed,
		noise:         *noise,
		offRouteAfter: *offRouteAfter,
		duration:      *duration,
	}); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// run sets the simulation up and reports whether every ride ended as expected.
func run(baseURL, gtfsSource, startDate, expectEnd string, requested []string, random, ridersPerTrip int, cfg *config) error {
	if cfg.interval <= 0 {
		return errors.New("-interval must be positive")
	}
	if cfg.speed <= 0 {
		return errors.New("-speed must be positive")
	}
	if ridersPerTrip < 1 {
		return errors.New("-riders-per-trip must be at least 1")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := &http.Client{Timeout: httpTimeout}
	static, err := rider.LoadStatic(ctx, gtfsSource, client)
	if err != nil {
		return fmt.Errorf("load GTFS: %w", err)
	}
	index, err := rider.BuildIndex(static, gtfsSource, time.Now())
	if err != nil {
		return fmt.Errorf("build index: %w", err)
	}
	if startDate == "" {
		startDate = index.ServiceDate(time.Now())
	}
	trips, err := pickTrips(index, requested, random, startDate)
	if err != nil {
		return err
	}

	cfg.api = &apiClient{base: baseURL, http: client}
	cfg.index = index
	cfg.startDate = startDate
	log.Printf("simulating %d trips × %d riders on %s: %v", len(trips), ridersPerTrip, startDate, trips)

	type result struct {
		trip, reason string
		err          error
	}
	results := make([]result, len(trips)*ridersPerTrip)

	var wg sync.WaitGroup
	for i, tripID := range trips {
		for j := range ridersPerTrip {
			slot := i*ridersPerTrip + j
			// Each rider gets its own source: math/rand's Rand is not safe for
			// concurrent use.
			rng := rand.New(rand.NewSource(int64(pickSeed + slot)))
			wg.Add(1)
			go func() {
				defer wg.Done()
				reason, err := runRider(ctx, cfg, tripID, rng)
				results[slot] = result{trip: tripID, reason: reason, err: err}
			}()
		}
	}
	wg.Wait()

	var failed int
	for _, res := range results {
		switch {
		case res.err != nil:
			log.Printf("%s: FAILED: %v", res.trip, res.err)
			failed++
		case expectEnd != "" && res.reason != expectEnd:
			log.Printf("%s: ended %q, expected %q", res.trip, res.reason, expectEnd)
			failed++
		default:
			log.Printf("%s: ended %q", res.trip, res.reason)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d rides did not end as expected", failed, len(results))
	}
	log.Printf("all %d rides ended as expected", len(results))
	return nil
}
