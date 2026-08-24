package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// LocationPruner periodically deletes location points older than the configured
// retention period, in bounded batches, until the backlog is drained.
//
// Shutdown follows the same shape as VehicleRateLimiter: a stop channel closed
// once by Stop, checked both between ticks and between delete batches so a long
// backlog does not delay server shutdown.
type LocationPruner struct {
	store     LocationPruneStore
	retention time.Duration
	interval  time.Duration
	batchSize int32

	stop chan struct{}
	once sync.Once
}

// NewLocationPruner starts the background pruning goroutine.
//
// Retention and interval must be positive and batchSize must be at least one.
// Invalid settings would make the pruner delete every stored location point, so
// it refuses to start and returns an inert pruner instead.
func NewLocationPruner(store LocationPruneStore, retention, interval time.Duration, batchSize int32) *LocationPruner {
	p := &LocationPruner{
		store:     store,
		retention: retention,
		interval:  interval,
		batchSize: batchSize,
		stop:      make(chan struct{}),
	}

	if retention <= 0 || interval <= 0 || batchSize < 1 {
		slog.Error("location pruner not started: invalid configuration",
			"retention", retention.String(), "interval", interval.String(), "batch_size", batchSize)
		return p
	}

	go p.run()
	return p
}

// Stop shuts down the background pruning goroutine. Safe to call more than once.
func (p *LocationPruner) Stop() {
	p.once.Do(func() { close(p.stop) })
}

func (p *LocationPruner) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// A background job outlives any single request, so it owns its own
			// context rather than borrowing a request's.
			p.pruneAll(context.Background())
		case <-p.stop:
			return
		}
	}
}

// pruneAll deletes expired rows in batches until a pass removes fewer rows than
// the batch size, the store returns an error, or the pruner is stopped.
func (p *LocationPruner) pruneAll(ctx context.Context) {
	start := time.Now()
	cutoff := start.Add(-p.retention)

	var total int64
	for {
		select {
		case <-p.stop:
			return
		default:
		}

		deleted, err := p.store.PruneLocationPoints(ctx, cutoff, p.batchSize)
		if err != nil {
			slog.Error("location retention prune failed",
				"error", err, "cutoff", cutoff, "deleted_so_far", total)
			return
		}

		total += deleted
		if deleted < int64(p.batchSize) {
			break
		}
	}

	if total > 0 {
		slog.Info("pruned expired location points",
			"deleted", total,
			"cutoff", cutoff,
			"retention", p.retention.String(),
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
		)
	}
}
