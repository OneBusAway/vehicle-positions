package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// LocationPruner periodically deletes location points older than the configured
// retention period, in bounded batches, until the backlog is drained.
//
// Shutdown follows the same shape as VehicleRateLimiter, with one addition: the
// worker owns a cancellable context, so Stop aborts a delete already running on
// the database rather than only stopping the next one.
type LocationPruner struct {
	store     LocationPruneStore
	retention time.Duration
	interval  time.Duration
	batchSize int32

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewLocationPruner starts the background pruning goroutine.
//
// Retention and interval must be positive and batchSize must be at least one.
// A non-positive retention would put the cutoff at "now" and delete every stored
// location point, so invalid settings are rejected rather than defaulted.
func NewLocationPruner(store LocationPruneStore, retention, interval time.Duration, batchSize int32) (*LocationPruner, error) {
	switch {
	case retention <= 0:
		return nil, fmt.Errorf("retention period must be positive, got %s", retention)
	case interval <= 0:
		return nil, fmt.Errorf("prune interval must be positive, got %s", interval)
	case batchSize < 1:
		return nil, fmt.Errorf("prune batch size must be at least 1, got %d", batchSize)
	}

	// A background job outlives any single request, so it owns its context
	// rather than borrowing a request's.
	ctx, cancel := context.WithCancel(context.Background())
	p := &LocationPruner{
		store:     store,
		retention: retention,
		interval:  interval,
		batchSize: batchSize,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	go p.run()
	return p, nil
}

// Stop cancels any prune already in flight and waits for the goroutine to exit.
// Safe to call more than once.
func (p *LocationPruner) Stop() {
	p.once.Do(p.cancel)
	<-p.done
}

func (p *LocationPruner) run() {
	defer close(p.done)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.pruneAll(p.ctx)
		case <-p.ctx.Done():
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
		if ctx.Err() != nil {
			return
		}

		deleted, err := p.store.PruneLocationPoints(ctx, cutoff, p.batchSize)
		if err != nil {
			// A delete aborted by shutdown is expected, not a failure worth
			// alarming on.
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
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
