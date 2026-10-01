package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// RefreshTokenPruner periodically deletes refresh tokens whose expires_at has
// passed.
//
// The lifecycle is LocationPruner's: the worker owns a cancellable context, so
// Stop aborts a delete already running on the database rather than only
// stopping the next one.
type RefreshTokenPruner struct {
	store    RefreshTokenPruneStore
	interval time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewRefreshTokenPruner starts the background pruning goroutine.
//
// There is no retention period to configure: each row's expires_at, set from
// REFRESH_TOKEN_TTL when the token was issued, already says when it stops
// being usable. A non-positive interval would panic time.NewTicker, so it is
// rejected rather than defaulted.
func NewRefreshTokenPruner(store RefreshTokenPruneStore, interval time.Duration) (*RefreshTokenPruner, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("refresh token prune interval must be positive, got %s", interval)
	}

	// A background job outlives any single request, so it owns its context
	// rather than borrowing a request's.
	ctx, cancel := context.WithCancel(context.Background())
	p := &RefreshTokenPruner{
		store:    store,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	go p.run()
	return p, nil
}

// Stop cancels any prune already in flight and waits for the goroutine to exit.
// Safe to call more than once.
func (p *RefreshTokenPruner) Stop() {
	p.once.Do(p.cancel)
	<-p.done
}

func (p *RefreshTokenPruner) run() {
	defer close(p.done)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.prune(p.ctx)
		case <-p.ctx.Done():
			return
		}
	}
}

// prune deletes every expired token in one statement. Unlike LocationPruner it
// does not batch: a pass removes only the tokens that expired since the last
// one — about one interval's worth of logins and refreshes — so the statement
// stays short without it.
func (p *RefreshTokenPruner) prune(ctx context.Context) {
	start := time.Now()

	deleted, err := p.store.PruneExpiredRefreshTokens(ctx, start)
	if err != nil {
		// A delete aborted by shutdown is expected, not a failure worth
		// alarming on.
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		slog.Error("refresh token prune failed", "error", err, "cutoff", start)
		return
	}

	if deleted > 0 {
		slog.Info("pruned expired refresh tokens",
			"deleted", deleted,
			"cutoff", start,
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
		)
	}
}
