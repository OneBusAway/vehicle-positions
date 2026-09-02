package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	// riderReapInterval is how often expired rides are ended and filed.
	riderReapInterval = 30 * time.Second
	// riderRetentionInterval is how often ride points past the retention
	// window are deleted.
	riderRetentionInterval = time.Hour
)

// Defaults for the rider-mode settings that have one (spec §4.1). They are
// named because each is used twice: once when reading the environment, and
// once as the fallback when a configured value could not be used.
const (
	defaultGTFSRefresh    = 24 * time.Hour
	defaultTrustedPoll    = 30 * time.Second
	defaultTrustedMaxAge  = 5 * time.Minute
	defaultRiderJWTTTL    = 8760 * time.Hour
	defaultPointRetention = 168 * time.Hour
)

// riderConfig is everything rider mode reads from the environment (spec §4.1).
// The engine itself never reads the environment: the thresholds it is tuned
// with are parsed here and handed to it.
type riderConfig struct {
	Enabled        bool
	GTFSSource     string // http(s) URL or local path to a GTFS static zip
	GTFSRefresh    time.Duration
	TrustedURLs    []string
	TrustedPoll    time.Duration
	TrustedMaxAge  time.Duration
	JWTTTL         time.Duration
	PointRetention time.Duration
	Thresholds     rider.Thresholds
}

// riderConfigFromEnv reads the rider-mode configuration. The only fatal
// misconfiguration is enabling rider mode without a schedule to match against;
// every other value falls back to its default, logging when what was supplied
// could not be parsed.
func riderConfigFromEnv() (riderConfig, error) {
	defaults := rider.DefaultThresholds()
	th := defaults
	// A non-positive distance or speed is not a stricter setting, it is a
	// broken one: zero metres rejects every point as off-route, and zero m/s
	// makes every movement implausible.
	th.MaxShapeDistance = envPositiveFloatOrDefault("RIDER_MAX_SHAPE_DISTANCE", defaults.MaxShapeDistance)
	th.MaxSpeed = envPositiveFloatOrDefault("RIDER_MAX_SPEED", defaults.MaxSpeed)
	th.ScheduleEarly = envDurationOrDefault("RIDER_SCHEDULE_EARLY", defaults.ScheduleEarly)
	th.ScheduleLate = envDurationOrDefault("RIDER_SCHEDULE_LATE", defaults.ScheduleLate)
	// Freshness of zero would expire every ride the moment it reported.
	th.PointMaxAge = envPositiveDurationOrDefault("RIDER_POINT_MAX_AGE", defaults.PointMaxAge)

	// Every duration below is one nothing downstream can survive a
	// non-positive value of, so each is clamped as it is read and the config
	// that comes back is valid by construction: a ticker interval of zero
	// panics, a non-positive staleness window discards every trusted entity, a
	// non-positive token lifetime issues tokens already expired, and a
	// non-positive retention window puts the deletion cutoff in the future and
	// takes every ride point with it.
	cfg := riderConfig{
		Enabled:        envBoolOrDefault("RIDER_MODE_ENABLED", false),
		GTFSSource:     os.Getenv("GTFS_STATIC_URL"),
		GTFSRefresh:    envPositiveDurationOrDefault("GTFS_STATIC_REFRESH", defaultGTFSRefresh),
		TrustedURLs:    splitURLs(os.Getenv("TRUSTED_GTFS_RT_URLS")),
		TrustedPoll:    envPositiveDurationOrDefault("TRUSTED_FEED_POLL", defaultTrustedPoll),
		TrustedMaxAge:  envPositiveDurationOrDefault("TRUSTED_FEED_MAX_AGE", defaultTrustedMaxAge),
		JWTTTL:         envPositiveDurationOrDefault("RIDER_JWT_TTL", defaultRiderJWTTTL),
		PointRetention: envPositiveDurationOrDefault("RIDER_POINT_RETENTION", defaultPointRetention),
		Thresholds:     th,
	}
	if cfg.Enabled && cfg.GTFSSource == "" {
		return riderConfig{}, errors.New("GTFS_STATIC_URL is required when RIDER_MODE_ENABLED is true")
	}
	return cfg, nil
}

// envFloatOrDefault reads a float setting, falling back to the default when it
// is absent or unparseable.
func envFloatOrDefault(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	// NaN and ±Inf parse, but a threshold compared against either is never
	// exceeded, which silently disables the check it configures.
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		slog.Warn("invalid float, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return f
}

// envPositiveDurationOrDefault reads a duration setting that must be positive.
// A value an operator can configure but nothing below can survive — a ticker
// interval, a freshness or staleness window, a token lifetime, a retention
// window — falls back to the default rather than disabling the thing it
// measures.
func envPositiveDurationOrDefault(key string, fallback time.Duration) time.Duration {
	d := envDurationOrDefault(key, fallback)
	if d <= 0 {
		slog.Warn("rider: duration must be positive, using default", "key", key, "value", d, "default", fallback)
		return fallback
	}
	return d
}

// envPositiveFloatOrDefault is envPositiveDurationOrDefault for the thresholds
// measured in metres and metres per second: zero metres rejects every point as
// off-route, and zero m/s makes every movement implausible.
func envPositiveFloatOrDefault(key string, fallback float64) float64 {
	v := envFloatOrDefault(key, fallback)
	if v <= 0 {
		slog.Warn("rider: value must be positive, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return v
}

// splitURLs parses a comma-separated URL list, dropping surrounding spaces and
// empty entries so that a trailing comma is not a feed.
func splitURLs(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if u := strings.TrimSpace(part); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// riderRuntime owns everything rider mode runs in the background: the schedule
// refresher, the trusted-feed poller, and the reap and retention tickers. It
// exists so main.go grows by one block: build it, hand its service to the
// router, and Stop it on shutdown.
type riderRuntime struct {
	cfg       riderConfig
	refresher *rider.Refresher
	trusted   *rider.TrustedFeed
	svc       *riderService
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// newRiderRuntime brings rider mode up (spec §4.1): load the schedule (a
// failure here aborts startup, since nothing can be verified without it), end
// every ride left active by the previous process, then wire the engine, the
// service and the background tickers. The returned runtime must be Stopped.
func newRiderRuntime(ctx context.Context, cfg riderConfig, store riderStore, jwtSecret []byte, trustProxy bool, tracker *Tracker) (*riderRuntime, error) {
	// http.DefaultClient carries no timeout of its own, which is what both
	// callers want: the GTFS download and each trusted-feed request apply
	// their own, and a static feed is far too large for a short one.
	client := http.DefaultClient

	index, err := rider.LoadIndex(ctx, cfg.GTFSSource, client, time.Now())
	if err != nil {
		return nil, err
	}
	slog.Info("rider: loaded GTFS", "source", cfg.GTFSSource, "trips", index.Stats().Trips,
		"shapes", index.Stats().Shapes, "timezone", index.Timezone().String())

	// Sessions live in memory only, so no ride survives a restart. Ending them
	// here is what lets a session trust its own history (spec §4.6).
	ended, err := store.EndAllActiveRides(ctx, "server_restart")
	if err != nil {
		return nil, err
	}
	if ended > 0 {
		slog.Info("rider: ended rides left active by the previous process", "count", ended)
	}

	refresher := rider.NewRefresher(index, func(ctx context.Context) (*rider.Index, error) {
		return rider.LoadIndex(ctx, cfg.GTFSSource, client, time.Now())
	})
	trusted := rider.NewTrustedFeed(cfg.TrustedURLs, client, cfg.TrustedMaxAge)
	agg := rider.NewAggregator(cfg.Thresholds, index.Timezone())
	svc := newRiderService(store, agg, refresher.Current, trustedSources{feed: trusted, tracker: tracker}, jwtSecret, cfg.JWTTTL, trustProxy)

	runCtx, cancel := context.WithCancel(ctx)
	rt := &riderRuntime{cfg: cfg, refresher: refresher, trusted: trusted, svc: svc, cancel: cancel}

	rt.goroutine(func() { refresher.Start(runCtx, cfg.GTFSRefresh) })
	if trusted.Configured() {
		rt.goroutine(func() { trusted.Start(runCtx, cfg.TrustedPoll) })
	}
	rt.goroutine(func() { rt.reapLoop(runCtx) })
	rt.goroutine(func() { rt.retentionLoop(runCtx, store) })
	return rt, nil
}

// goroutine runs fn in the background and records it, so Stop can wait for it.
func (rt *riderRuntime) goroutine(fn func()) {
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		fn()
	}()
}

// reapLoop ends rides that have gone quiet or run too long and files what they
// amounted to. Reap ends its sessions in place and leaves them registered, so
// each one is filed through finishRide — the single ride-ending path — which
// keeps the session's own end reason and unregisters it only once the store
// has accepted the outcome. A write that fails leaves the ride registered and
// ended, so the next tick simply tries again.
func (rt *riderRuntime) reapLoop(ctx context.Context) {
	rider.TickUntilDone(ctx, riderReapInterval, func(now time.Time) {
		for _, rideID := range rt.svc.agg.Reap(now) {
			// Every id Reap returns names an already-ended session, so
			// finishRide files it under the reason the session ended with;
			// this one is never the one recorded.
			if _, err := rt.svc.finishRide(ctx, rideID, rider.EndIdle); err != nil && !errors.Is(err, errRideNotActive) {
				slog.Error("rider: could not file a reaped ride, retrying next sweep",
					"ride_id", rideID, "error", err)
			}
		}
	})
}

// retentionLoop enforces the ride-point retention window (spec §4.1).
func (rt *riderRuntime) retentionLoop(ctx context.Context, store RidePointPruner) {
	rider.TickUntilDone(ctx, riderRetentionInterval, func(now time.Time) {
		deleted, err := store.DeleteRidePointsBefore(ctx, now.Add(-rt.cfg.PointRetention))
		if err != nil {
			slog.Warn("rider: ride point retention sweep failed", "error", err)
			return
		}
		if deleted > 0 {
			slog.Info("rider: deleted expired ride points", "count", deleted)
		}
	})
}

// Stop shuts the background loops down and waits for them, then stops the
// service's rate limiters. It must be called exactly once.
func (rt *riderRuntime) Stop() {
	rt.cancel()
	rt.wg.Wait()
	rt.svc.Stop()
}

// trustedSources is the agency's own view of where its trips are, as the rider
// API sees it: the configured trusted feeds, and behind them this server's own
// driver Tracker. A trip a driver is reporting through this server is covered
// exactly as one an external feed reports — the feed must never carry a rider
// estimate beside it (spec §3), and rider points are corroborated against it —
// without the operator having to point TRUSTED_GTFS_RT_URLS back at the
// server itself.
type trustedSources struct {
	feed    *rider.TrustedFeed
	tracker *Tracker // nil when there is no local driver half to consult
}

func (t trustedSources) Configured() bool           { return t.feed.Configured() }
func (t trustedSources) Health() []rider.FeedHealth { return t.feed.Health() }

func (t trustedSources) Lookup(key rider.TripKey, now time.Time) (rider.TrustedVehicle, bool) {
	if v, ok := t.feed.Lookup(key, now); ok {
		return v, true
	}
	if t.tracker == nil {
		return rider.TrustedVehicle{}, false
	}
	v, ok := t.tracker.VehicleOnTrip(key.TripID)
	if !ok {
		return rider.TrustedVehicle{}, false
	}
	return rider.TrustedVehicle{
		VehicleID: v.VehicleID,
		Pos:       rider.LatLon{Lat: v.Latitude, Lon: v.Longitude},
		Timestamp: time.Unix(v.Timestamp, 0),
	}, true
}

func (t trustedSources) Covers(key rider.TripKey, now time.Time) bool {
	_, ok := t.Lookup(key, now)
	return ok
}

// riderOff stands in for the rider service when rider mode is off: it has no
// estimates to contribute to the feed, and reports itself as disabled. It
// exists so the handlers never have to ask whether rider mode is on — being
// off is just another answer, given by a value rather than by a nil check.
type riderOff struct{}

// Estimates: a server with no rider engine has no rider-derived positions.
func (riderOff) Estimates(time.Time) []rider.TripEstimate { return nil }

// RiderStatus: the zero riderStatusResponse is exactly {"enabled":false}.
func (riderOff) RiderStatus(context.Context) (riderStatusResponse, error) {
	return riderStatusResponse{}, nil
}

// riderOrOff is the rider service, or the null object when rider mode is off.
// It exists because passing a nil *riderService straight through would hand a
// handler a non-nil interface holding a nil pointer.
func riderOrOff(svc *riderService) (estimateSource, riderStatusProvider) {
	if svc == nil {
		return riderOff{}, riderOff{}
	}
	return svc, svc
}
