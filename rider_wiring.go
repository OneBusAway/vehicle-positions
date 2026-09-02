package main

import (
	"context"
	"errors"
	"log/slog"
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
	th.MaxShapeDistance = positiveFloatOr("RIDER_MAX_SHAPE_DISTANCE",
		envFloatOrDefault("RIDER_MAX_SHAPE_DISTANCE", defaults.MaxShapeDistance), defaults.MaxShapeDistance)
	th.MaxSpeed = positiveFloatOr("RIDER_MAX_SPEED",
		envFloatOrDefault("RIDER_MAX_SPEED", defaults.MaxSpeed), defaults.MaxSpeed)
	th.ScheduleEarly = envDurationOrDefault("RIDER_SCHEDULE_EARLY", defaults.ScheduleEarly)
	th.ScheduleLate = envDurationOrDefault("RIDER_SCHEDULE_LATE", defaults.ScheduleLate)
	// Freshness of zero would expire every ride the moment it reported.
	th.PointMaxAge = positiveOr("RIDER_POINT_MAX_AGE",
		envDurationOrDefault("RIDER_POINT_MAX_AGE", defaults.PointMaxAge), defaults.PointMaxAge)

	cfg := riderConfig{
		Enabled:     envBoolOrFalse("RIDER_MODE_ENABLED"),
		GTFSSource:  os.Getenv("GTFS_STATIC_URL"),
		GTFSRefresh: envDurationOrDefault("GTFS_STATIC_REFRESH", defaultGTFSRefresh),
		TrustedURLs: splitURLs(os.Getenv("TRUSTED_GTFS_RT_URLS")),
		TrustedPoll: envDurationOrDefault("TRUSTED_FEED_POLL", defaultTrustedPoll),
		// A non-positive staleness window would discard every trusted entity;
		// a non-positive token lifetime would issue tokens already expired.
		TrustedMaxAge:  positiveOr("TRUSTED_FEED_MAX_AGE", envDurationOrDefault("TRUSTED_FEED_MAX_AGE", defaultTrustedMaxAge), defaultTrustedMaxAge),
		JWTTTL:         positiveOr("RIDER_JWT_TTL", envDurationOrDefault("RIDER_JWT_TTL", defaultRiderJWTTTL), defaultRiderJWTTTL),
		PointRetention: envDurationOrDefault("RIDER_POINT_RETENTION", defaultPointRetention),
		Thresholds:     th,
	}
	if cfg.Enabled && cfg.GTFSSource == "" {
		return riderConfig{}, errors.New("GTFS_STATIC_URL is required when RIDER_MODE_ENABLED is true")
	}
	return cfg, nil
}

// envBoolOrFalse reads a boolean setting. Anything that does not parse is
// treated as "off": rider mode is opt-in, so a typo must not enable it.
func envBoolOrFalse(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("invalid boolean, treating as false", "key", key, "value", v)
		return false
	}
	return b
}

// envFloatOrDefault reads a float setting, falling back to the default when it
// is absent or unparseable.
func envFloatOrDefault(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Warn("invalid float, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return f
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
func newRiderRuntime(ctx context.Context, cfg riderConfig, store riderStore, jwtSecret []byte, trustProxy bool) (*riderRuntime, error) {
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
	svc := newRiderService(store, agg, refresher.Current, trusted, jwtSecret, cfg.JWTTTL, trustProxy, cfg.Thresholds)

	runCtx, cancel := context.WithCancel(ctx)
	rt := &riderRuntime{cfg: cfg, refresher: refresher, trusted: trusted, svc: svc, cancel: cancel}

	refresh := positiveOr("GTFS_STATIC_REFRESH", cfg.GTFSRefresh, defaultGTFSRefresh)
	rt.goroutine(func() { refresher.Start(runCtx, refresh) })
	if trusted.Configured() {
		poll := positiveOr("TRUSTED_FEED_POLL", cfg.TrustedPoll, defaultTrustedPoll)
		rt.goroutine(func() { trusted.Start(runCtx, poll) })
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

// positiveOr rejects a duration an operator can configure but nothing below
// can survive: rider.Refresher.Start and rider.TrustedFeed.Start build a
// time.Ticker, which panics on a non-positive interval; a non-positive
// retention window would put the deletion cutoff in the future and take every
// ride point with it; and a non-positive freshness, staleness or token
// lifetime would silently disable the thing it measures.
func positiveOr(key string, d, fallback time.Duration) time.Duration {
	if d <= 0 {
		slog.Warn("rider: duration must be positive, using default", "key", key, "value", d, "default", fallback)
		return fallback
	}
	return d
}

// positiveFloatOr is positiveOr for the thresholds measured in metres and
// metres per second.
func positiveFloatOr(key string, v, fallback float64) float64 {
	if v <= 0 {
		slog.Warn("rider: value must be positive, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return v
}

// reapLoop ends rides that have gone quiet or run too long and files what they
// amounted to. Reap has already ended and unregistered each session it returns,
// so the aggregator no longer knows about them: they are persisted through
// fileReaped rather than finishRide.
func (rt *riderRuntime) reapLoop(ctx context.Context) {
	ticker := time.NewTicker(riderReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, sess := range rt.svc.agg.Reap(now) {
				if err := rt.svc.fileReaped(ctx, sess); err != nil {
					// The session is gone from the aggregator and cannot be
					// put back — re-adding it would republish a ride that has
					// already ended. The ride's row stays `active` until the
					// next restart's EndAllActiveRides closes it, and the
					// admin rides list shows it in the meantime.
					slog.Error("rider: could not file a reaped ride, dropping it",
						"ride_id", sess.ID(), "rider_id", sess.RiderID(), "error", err)
				}
			}
		}
	}
}

// retentionLoop enforces the ride-point retention window (spec §4.1).
func (rt *riderRuntime) retentionLoop(ctx context.Context, store RidePointPruner) {
	retention := positiveOr("RIDER_POINT_RETENTION", rt.cfg.PointRetention, defaultPointRetention)
	ticker := time.NewTicker(riderRetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := store.DeleteRidePointsBefore(ctx, time.Now().Add(-retention))
			if err != nil {
				slog.Warn("rider: ride point retention sweep failed", "error", err)
				continue
			}
			if deleted > 0 {
				slog.Info("rider: deleted expired ride points", "count", deleted)
			}
		}
	}
}

// Stop shuts the background loops down and waits for them, then stops the
// service's rate limiters. It must be called exactly once.
func (rt *riderRuntime) Stop() {
	rt.cancel()
	rt.wg.Wait()
	rt.svc.Stop()
}

// estimatesOrNil is the rider service as a feed estimate source, or a true nil
// interface when rider mode is off. Passing the *riderService straight through
// would hand handleGetFeed a non-nil interface holding a nil pointer.
func estimatesOrNil(svc *riderService) estimateSource {
	if svc == nil {
		return nil
	}
	return svc
}

// statusOrNil is the rider service as an admin status provider, or a true nil
// interface when rider mode is off — which handleRiderAdminStatus reports as
// {"enabled":false}.
func statusOrNil(svc *riderService) riderStatusProvider {
	if svc == nil {
		return nil
	}
	return svc
}
