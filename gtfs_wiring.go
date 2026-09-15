package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

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
// `refresh`. A failure to load is fatal by policy (spec §4.1), even when only
// the driver catalog needs the schedule and rider mode is off, so a
// misconfigured feed is noticed at deploy time rather than as an empty
// picker. A failed reload keeps the previous index and logs.
func newGTFSRuntime(ctx context.Context, source string, refresh time.Duration) (*gtfsRuntime, error) {
	// http.DefaultClient carries no timeout of its own, which is what the
	// download wants: it applies its own, and a static feed is far too large
	// for a short one.
	client := http.DefaultClient

	index, err := rider.LoadIndex(ctx, source, client, time.Now())
	if err != nil {
		return nil, err
	}
	stats := index.Stats()
	slog.Info("gtfs: loaded schedule", "source", source, "routes", stats.Routes, "trips", stats.Trips,
		"shapes", stats.Shapes, "timezone", index.Timezone().String())

	refresher := rider.NewRefresher(index, func(ctx context.Context) (*rider.Index, error) {
		return rider.LoadIndex(ctx, source, client, time.Now())
	})

	runCtx, cancel := context.WithCancel(ctx)
	rt := &gtfsRuntime{refresher: refresher, cancel: cancel}
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		refresher.Start(runCtx, refresh)
	}()
	return rt, nil
}

// Index returns the schedule in force right now.
func (rt *gtfsRuntime) Index() *rider.Index { return rt.refresher.Current() }

// Stop ends the refresh loop and waits for it. It must be called exactly once.
func (rt *gtfsRuntime) Stop() {
	rt.cancel()
	rt.wg.Wait()
}
