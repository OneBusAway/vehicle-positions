package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gtfsFeedCache keeps the last GTFS static zip that loaded cleanly, so the
// server can start on it while the agency's GTFS host is down.
type gtfsFeedCache interface {
	LoadGTFSFeed(ctx context.Context) (*CachedGTFSFeed, error)
	SaveGTFSFeed(ctx context.Context, source string, body []byte) error
}

// gtfsRuntime owns the schedule: the index loaded at startup and the
// background refresh that replaces it. It stands on its own because two
// consumers read the schedule — rider mode and the driver catalog — and
// neither should have to own it for the other. The returned runtime must be
// Stopped.
type gtfsRuntime struct {
	refresher *rider.Refresher
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// newGTFSRuntime loads the feed at source and starts reloading it every
// `refresh`. Every feed that loads cleanly is saved to cache (which may be
// nil). When the feed cannot be loaded at startup the server starts on the
// cached copy of the same source instead, with a WARN, so an outage at the
// agency's GTFS host cannot keep the realtime feed down. With no usable cached
// copy a failure to load is fatal by policy (spec §4.1), even when only the
// driver catalog needs the schedule and rider mode is off, so a misconfigured
// feed is noticed at deploy time rather than as an empty picker. A failed
// reload keeps the previous index and logs.
func newGTFSRuntime(ctx context.Context, source string, refresh time.Duration, cache gtfsFeedCache) (*gtfsRuntime, error) {
	// http.DefaultClient carries no timeout of its own, which is what the
	// download wants: it applies its own, and a static feed is far too large
	// for a short one.
	client := http.DefaultClient

	load := func(ctx context.Context) (*rider.Index, error) {
		body, err := rider.FetchFeed(ctx, source, client)
		if err != nil {
			return nil, err
		}
		index, err := rider.IndexFromBytes(body, source, time.Now())
		if err != nil {
			return nil, err
		}
		if cache != nil {
			// The schedule is already loaded; a cache that cannot be
			// written only costs the fallback, not this load.
			if err := cache.SaveGTFSFeed(ctx, source, body); err != nil {
				slog.Warn("gtfs: could not cache the schedule for a later outage", "error", err)
			}
		}
		return index, nil
	}

	index, err := load(ctx)
	if err != nil {
		cached, cacheErr := loadCachedGTFS(ctx, cache, source)
		if cacheErr != nil {
			return nil, fmt.Errorf("%w (and no cached copy to fall back on: %v)", err, cacheErr)
		}
		slog.Warn("gtfs: could not load the schedule, starting on the cached copy",
			"source", source, "cached_at", cached.Stats().LoadedAt, "error", err)
		index = cached
	}
	stats := index.Stats()
	slog.Info("gtfs: loaded schedule", "source", source, "routes", stats.Routes, "trips", stats.Trips,
		"shapes", stats.Shapes, "stop_shapes", stats.StopShapes, "timezone", index.Timezone().String())

	refresher := rider.NewRefresher(index, load)

	runCtx, cancel := context.WithCancel(ctx)
	rt := &gtfsRuntime{refresher: refresher, cancel: cancel}
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		refresher.Start(runCtx, refresh)
	}()
	return rt, nil
}

// loadCachedGTFS indexes the cached feed, which must be from source: a copy
// of a feed the operator has since replaced is not the schedule they asked
// for. The index's LoadedAt is when the cached copy was downloaded.
func loadCachedGTFS(ctx context.Context, cache gtfsFeedCache, source string) (*rider.Index, error) {
	if cache == nil {
		return nil, ErrNoCachedGTFSFeed
	}
	feed, err := cache.LoadGTFSFeed(ctx)
	if err != nil {
		return nil, err
	}
	if feed.Source != source {
		return nil, fmt.Errorf("the cached copy is of %s, not %s", feed.Source, source)
	}
	return rider.IndexFromBytes(feed.Body, source, feed.FetchedAt)
}

// Index returns the schedule in force right now.
func (rt *gtfsRuntime) Index() *rider.Index { return rt.refresher.Current() }

// Stop ends the refresh loop and waits for it. It must be called exactly once.
func (rt *gtfsRuntime) Stop() {
	rt.cancel()
	rt.wg.Wait()
}
