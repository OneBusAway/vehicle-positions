package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pruneResult struct {
	deleted int64
	err     error
}

// fakeTokenPruneStore records each cutoff and replays scripted results, one
// per call; once they run out, calls report nothing deleted. It fails the way
// the Store does — errors arrive wrapped, and a cancelled context fails the
// call as pgx would — so the pruner is never tested against a store more
// forgiving than the real one. The pruner calls it from its background
// goroutine, so every field is guarded by mu. The refresh tokens' calls are
// results and cutoffs.
type fakeTokenPruneStore struct {
	mu      sync.Mutex
	results []pruneResult
	cutoffs []time.Time

	revocationResults []pruneResult
	revocationCutoffs []time.Time
}

func (f *fakeTokenPruneStore) PruneExpiredRefreshTokens(ctx context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return replay(ctx, "prune expired refresh tokens", cutoff, &f.cutoffs, f.results)
}

func (f *fakeTokenPruneStore) PruneExpiredRevocations(ctx context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return replay(ctx, "prune expired revocations", cutoff, &f.revocationCutoffs, f.revocationResults)
}

func replay(ctx context.Context, call string, cutoff time.Time, cutoffs *[]time.Time, results []pruneResult) (int64, error) {
	index := len(*cutoffs)
	*cutoffs = append(*cutoffs, cutoff)

	var result pruneResult
	if index < len(results) {
		result = results[index]
	}
	if result.err == nil {
		result.err = ctx.Err()
	}
	if result.err != nil {
		return 0, fmt.Errorf("%s: %w", call, result.err)
	}
	return result.deleted, nil
}

func (f *fakeTokenPruneStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

func (f *fakeTokenPruneStore) firstCutoff(t *testing.T) time.Time {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.cutoffs, "store was never called")
	return f.cutoffs[0]
}

// newUnstartedTokenPruner builds a pruner without its background goroutine,
// so a single pass can be driven and asserted with no ticker firing underneath
// it. done starts closed because there is no goroutine for Stop to wait on.
func newUnstartedTokenPruner(store TokenPruneStore, interval time.Duration) *TokenPruner {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	return &TokenPruner{
		store:    store,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		done:     done,
	}
}

func TestNewTokenPruner_RejectsNonPositiveInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		wantErr  string
	}{
		{name: "zero", interval: 0, wantErr: "token prune interval must be positive, got 0s"},
		{name: "negative", interval: -time.Minute, wantErr: "token prune interval must be positive, got -1m0s"},
		{name: "smallest positive", interval: time.Nanosecond},
		{name: "default", interval: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeTokenPruneStore{}
			pruner, err := NewTokenPruner(fake, tt.interval)

			if tt.wantErr == "" {
				require.NoError(t, err)
				require.NotNil(t, pruner)
				pruner.Stop()
				return
			}

			require.Error(t, err, "a non-positive interval must be rejected, not defaulted")
			assert.Equal(t, tt.wantErr, err.Error())
			assert.Nil(t, pruner, "no pruner should be handed back to the caller")
			assert.Never(t, func() bool {
				return fake.callCount() > 0
			}, 50*time.Millisecond, 5*time.Millisecond, "rejected configuration must never prune")
		})
	}
}

func TestTokenPruner_PrunesOnTick(t *testing.T) {
	fake := &fakeTokenPruneStore{}
	pruner, err := NewTokenPruner(fake, 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() > 0
	}, time.Second, 5*time.Millisecond, "pruner should prune on its interval")
}

// TestTokenPruner_CutoffIsTheTimeOfThePass uses an hour-long interval so
// that a cutoff offset by the interval in either direction falls outside the
// bounds. An earlier cutoff only delays cleanup; a later one would delete
// tokens that can still be refreshed and sign drivers out mid-shift.
func TestTokenPruner_CutoffIsTheTimeOfThePass(t *testing.T) {
	fake := &fakeTokenPruneStore{}
	pruner := newUnstartedTokenPruner(fake, time.Hour)

	before := time.Now()
	pruner.prune(pruner.ctx)
	after := time.Now()

	cutoff := fake.firstCutoff(t)
	assert.False(t, cutoff.Before(before), "cutoff %s is earlier than the pass that used it", cutoff)
	assert.False(t, cutoff.After(after), "a cutoff in the future would delete refresh tokens that are still valid")
}

// TestTokenPruner_KeepsRevocationsAWhilePastTheirTokens: a revocation goes only
// once its token is long refused for its own expiry. A later cutoff would let a
// token that was signed out be used again by a request under way as it expired,
// or after the clock is set back.
func TestTokenPruner_KeepsRevocationsAWhilePastTheirTokens(t *testing.T) {
	fake := &fakeTokenPruneStore{}
	pruner := newUnstartedTokenPruner(fake, 24*time.Hour)

	before := time.Now()
	pruner.prune(pruner.ctx)

	require.Len(t, fake.revocationCutoffs, 1)
	assert.WithinRange(t, fake.revocationCutoffs[0], before.Add(-time.Hour), time.Now().Add(-time.Hour))
}

// TestTokenPruner_OneTableFailingLeavesTheOther: the revocations are pruned
// though the refresh tokens can't be, and the failure names what failed.
// Not safe for t.Parallel(); uses global logger.
func TestTokenPruner_OneTableFailingLeavesTheOther(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeTokenPruneStore
		want  []map[string]any
	}{
		{
			"the refresh tokens",
			&fakeTokenPruneStore{results: []pruneResult{{err: errors.New("statement timeout")}}, revocationResults: []pruneResult{{deleted: 4}}},
			[]map[string]any{
				{"level": "ERROR", "msg": "refresh token prune failed"},
				{"level": "INFO", "msg": "pruned expired revocations", "deleted": float64(4)},
			},
		},
		{
			"the revocations",
			&fakeTokenPruneStore{results: []pruneResult{{deleted: 2}}, revocationResults: []pruneResult{{err: errors.New("statement timeout")}}},
			[]map[string]any{
				{"level": "INFO", "msg": "pruned expired refresh tokens", "deleted": float64(2)},
				{"level": "ERROR", "msg": "revocation prune failed"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			original := slog.Default()
			t.Cleanup(func() { slog.SetDefault(original) })
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

			pruner := newUnstartedTokenPruner(tt.store, time.Hour)
			pruner.prune(pruner.ctx)

			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			require.Len(t, lines, len(tt.want))
			for i, want := range tt.want {
				var entry map[string]any
				require.NoError(t, json.Unmarshal(lines[i], &entry))
				for key, value := range want {
					assert.Equal(t, value, entry[key], key)
				}
			}
		})
	}
}

// TestTokenPruner_StopEndsTheRevocationsToo: once the pruner is stopped, neither
// table's delete goes through.
// Not safe for t.Parallel(); uses global logger.
func TestTokenPruner_StopEndsTheRevocationsToo(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	fake := &fakeTokenPruneStore{results: []pruneResult{{deleted: 2}}, revocationResults: []pruneResult{{deleted: 4}}}
	pruner := newUnstartedTokenPruner(fake, time.Hour)
	pruner.Stop()
	pruner.prune(pruner.ctx)

	require.Len(t, fake.revocationCutoffs, 1)
	assert.Empty(t, buf.String(), "nothing was deleted, and it was not a failure")
}

func TestTokenPruner_StopIsIdempotent(t *testing.T) {
	pruner, err := NewTokenPruner(&fakeTokenPruneStore{}, time.Hour)
	require.NoError(t, err)

	returned := make(chan struct{})
	go func() {
		pruner.Stop()
		pruner.Stop()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a second Stop must return, not block or panic")
	}
}

// blockingTokenPruneStore blocks until its context is cancelled, standing in
// for a slow or stuck DELETE on the database, then fails the way the real
// store does.
type blockingTokenPruneStore struct {
	enterOnce sync.Once
	entered   chan struct{}

	mu  sync.Mutex
	err error
}

func (b *blockingTokenPruneStore) PruneExpiredRefreshTokens(ctx context.Context, _ time.Time) (int64, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-ctx.Done()

	b.mu.Lock()
	b.err = ctx.Err()
	b.mu.Unlock()
	return 0, fmt.Errorf("prune expired refresh tokens: %w", ctx.Err())
}

func (b *blockingTokenPruneStore) PruneExpiredRevocations(ctx context.Context, _ time.Time) (int64, error) {
	return 0, fmt.Errorf("prune expired revocations: %w", ctx.Err())
}

func (b *blockingTokenPruneStore) observedErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func TestTokenPruner_StopCancelsInFlightDelete(t *testing.T) {
	blocking := &blockingTokenPruneStore{entered: make(chan struct{})}
	pruner, err := NewTokenPruner(blocking, time.Millisecond)
	require.NoError(t, err)

	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("prune never started")
	}

	stopped := make(chan struct{})
	go func() {
		pruner.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return: the in-flight delete was never cancelled")
	}

	assert.ErrorIs(t, blocking.observedErr(), context.Canceled,
		"the running delete should see its context cancelled, not run to completion")
}

func TestTokenPruner_StoreErrorDoesNotKillTheLoop(t *testing.T) {
	fake := &fakeTokenPruneStore{results: []pruneResult{{err: errors.New("connection refused")}}}
	pruner, err := NewTokenPruner(fake, 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() >= 2
	}, time.Second, 5*time.Millisecond, "a failed pass must not stop later passes")
}

// Not safe for t.Parallel(); uses global logger
func TestTokenPruner_CancellationIsNotLoggedAsError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		shutdown  bool
		wantError bool
	}{
		{name: "delete cancelled by Stop", err: context.Canceled, shutdown: true},
		// pgx can surface a cancelled query as a connection error that does not
		// wrap context.Canceled, so the pruner's own context is checked too.
		{name: "other error after Stop", err: errors.New("conn closed"), shutdown: true},
		// The control case: without it, a broken log capture would pass the
		// two above vacuously.
		{name: "genuine failure", err: errors.New("connection refused"), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			original := slog.Default()
			t.Cleanup(func() { slog.SetDefault(original) })
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

			fake := &fakeTokenPruneStore{results: []pruneResult{{err: tt.err}}}
			pruner := newUnstartedTokenPruner(fake, time.Hour)
			if tt.shutdown {
				pruner.Stop()
			}

			pruner.prune(pruner.ctx)

			require.Equal(t, 1, fake.callCount())
			if !tt.wantError {
				assert.Empty(t, buf.String(), "a delete aborted by shutdown is expected and must not be logged")
				return
			}

			var entry map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &entry), "a genuine failure must be logged")
			assert.Equal(t, "ERROR", entry["level"])
			assert.Equal(t, "refresh token prune failed", entry["msg"])
			assert.Contains(t, entry["error"], "connection refused")
		})
	}
}

// Not safe for t.Parallel(); uses global logger
func TestTokenPruner_LogsOnlyWhenRowsDeleted(t *testing.T) {
	t.Run("nothing expired", func(t *testing.T) {
		var buf bytes.Buffer
		original := slog.Default()
		t.Cleanup(func() { slog.SetDefault(original) })
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

		fake := &fakeTokenPruneStore{results: []pruneResult{{deleted: 0}}}
		pruner := newUnstartedTokenPruner(fake, time.Hour)

		pruner.prune(pruner.ctx)

		require.Equal(t, 1, fake.callCount())
		assert.Empty(t, buf.String(), "an idle server must not log a no-op line every interval")
	})

	t.Run("rows deleted", func(t *testing.T) {
		var buf bytes.Buffer
		original := slog.Default()
		t.Cleanup(func() { slog.SetDefault(original) })
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

		fake := &fakeTokenPruneStore{results: []pruneResult{{deleted: 3}}}
		pruner := newUnstartedTokenPruner(fake, time.Hour)

		pruner.prune(pruner.ctx)

		var entry map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
		assert.Equal(t, "INFO", entry["level"])
		assert.Equal(t, "pruned expired refresh tokens", entry["msg"])
		assert.Equal(t, float64(3), entry["deleted"])
		assert.IsType(t, float64(0), entry["duration_ms"],
			"duration must be numeric so log tooling can filter and sort on it")
	})
}
