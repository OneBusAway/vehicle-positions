package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// revocationGrace is how long a revocation is kept once its token has expired.
// A token is refused from then on whether or not it is revoked, but by its own
// expiry: a request whose token was read just before it, and a clock that is
// set back, would find a revoked token good again without the row.
const revocationGrace = time.Hour

// TokenPruneStore is what the token pruner deletes through.
type TokenPruneStore interface {
	RefreshTokenPruneStore
	RevocationPruneStore
}

// TokenPruner periodically deletes the refresh tokens, and the revocations of
// access tokens, whose expires_at has passed.
//
// The lifecycle is LocationPruner's: the worker owns a cancellable context, so
// Stop aborts a delete already running on the database rather than only
// stopping the next one.
type TokenPruner struct {
	store    TokenPruneStore
	interval time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewTokenPruner starts the background pruning goroutine.
//
// There is no retention period to configure: each row's expires_at, its token's,
// already says when that stops being usable. A non-positive interval would panic
// time.NewTicker, so it is rejected rather than defaulted.
func NewTokenPruner(store TokenPruneStore, interval time.Duration) (*TokenPruner, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("token prune interval must be positive, got %s", interval)
	}

	// A background job outlives any single request, so it owns its context
	// rather than borrowing a request's.
	ctx, cancel := context.WithCancel(context.Background())
	p := &TokenPruner{
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
func (p *TokenPruner) Stop() {
	p.once.Do(p.cancel)
	<-p.done
}

func (p *TokenPruner) run() {
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

// prune deletes the expired rows of each table, whether or not the other's
// deletes fail.
func (p *TokenPruner) prune(ctx context.Context) {
	now := time.Now()
	pruneExpired(ctx, "refresh token", now, p.store.PruneExpiredRefreshTokens)
	pruneExpired(ctx, "revocation", now.Add(-revocationGrace), p.store.PruneExpiredRevocations)
}

// pruneExpired deletes every row that expired before cutoff in one statement.
// Unlike LocationPruner it does not batch: a pass removes only the rows that
// expired since the last one, about one interval's worth of logins, refreshes
// and logouts, so the statement stays short without it.
func pruneExpired(ctx context.Context, what string, cutoff time.Time, prune func(context.Context, time.Time) (int64, error)) {
	start := time.Now()

	deleted, err := prune(ctx, cutoff)
	if err != nil {
		// A delete aborted by shutdown is expected, not a failure worth
		// alarming on.
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		slog.Error(what+" prune failed", "error", err, "cutoff", cutoff)
		return
	}

	if deleted > 0 {
		slog.Info("pruned expired "+what+"s",
			"deleted", deleted,
			"cutoff", cutoff,
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
		)
	}
}
