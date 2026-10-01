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

type refreshPruneResult struct {
	deleted int64
	err     error
}

// fakeRefreshPruneStore records each cutoff and replays scripted results, one
// per call; once they run out, calls report nothing deleted. It fails the way
// Store.PruneExpiredRefreshTokens does — errors arrive wrapped, and a cancelled
// context fails the call as pgx would — so the pruner is never tested against
// a store more forgiving than the real one. The pruner calls it from its
// background goroutine, so every field is guarded by mu.
type fakeRefreshPruneStore struct {
	mu      sync.Mutex
	results []refreshPruneResult
	cutoffs []time.Time
}

func (f *fakeRefreshPruneStore) PruneExpiredRefreshTokens(ctx context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := len(f.cutoffs)
	f.cutoffs = append(f.cutoffs, cutoff)

	var result refreshPruneResult
	if index < len(f.results) {
		result = f.results[index]
	}
	if result.err == nil {
		result.err = ctx.Err()
	}
	if result.err != nil {
		return 0, fmt.Errorf("prune expired refresh tokens: %w", result.err)
	}
	return result.deleted, nil
}

func (f *fakeRefreshPruneStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

func (f *fakeRefreshPruneStore) firstCutoff(t *testing.T) time.Time {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.cutoffs, "store was never called")
	return f.cutoffs[0]
}

// newUnstartedRefreshPruner builds a pruner without its background goroutine,
// so a single pass can be driven and asserted with no ticker firing underneath
// it. done starts closed because there is no goroutine for Stop to wait on.
func newUnstartedRefreshPruner(store RefreshTokenPruneStore, interval time.Duration) *RefreshTokenPruner {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	return &RefreshTokenPruner{
		store:    store,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		done:     done,
	}
}

func TestNewRefreshTokenPruner_RejectsNonPositiveInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		wantErr  string
	}{
		{name: "zero", interval: 0, wantErr: "refresh token prune interval must be positive, got 0s"},
		{name: "negative", interval: -time.Minute, wantErr: "refresh token prune interval must be positive, got -1m0s"},
		{name: "smallest positive", interval: time.Nanosecond},
		{name: "default", interval: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRefreshPruneStore{}
			pruner, err := NewRefreshTokenPruner(fake, tt.interval)

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

func TestRefreshTokenPruner_PrunesOnTick(t *testing.T) {
	fake := &fakeRefreshPruneStore{}
	pruner, err := NewRefreshTokenPruner(fake, 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() > 0
	}, time.Second, 5*time.Millisecond, "pruner should prune on its interval")
}

// TestRefreshTokenPruner_CutoffIsTheTimeOfThePass uses an hour-long interval so
// that a cutoff offset by the interval in either direction falls outside the
// bounds. An earlier cutoff only delays cleanup; a later one would delete
// tokens that can still be refreshed and sign drivers out mid-shift.
func TestRefreshTokenPruner_CutoffIsTheTimeOfThePass(t *testing.T) {
	fake := &fakeRefreshPruneStore{}
	pruner := newUnstartedRefreshPruner(fake, time.Hour)

	before := time.Now()
	pruner.prune(pruner.ctx)
	after := time.Now()

	cutoff := fake.firstCutoff(t)
	assert.False(t, cutoff.Before(before), "cutoff %s is earlier than the pass that used it", cutoff)
	assert.False(t, cutoff.After(after), "a cutoff in the future would delete refresh tokens that are still valid")
}

func TestRefreshTokenPruner_StopIsIdempotent(t *testing.T) {
	pruner, err := NewRefreshTokenPruner(&fakeRefreshPruneStore{}, time.Hour)
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

// blockingRefreshPruneStore blocks until its context is cancelled, standing in
// for a slow or stuck DELETE on the database, then fails the way the real
// store does.
type blockingRefreshPruneStore struct {
	enterOnce sync.Once
	entered   chan struct{}

	mu  sync.Mutex
	err error
}

func (b *blockingRefreshPruneStore) PruneExpiredRefreshTokens(ctx context.Context, _ time.Time) (int64, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-ctx.Done()

	b.mu.Lock()
	b.err = ctx.Err()
	b.mu.Unlock()
	return 0, fmt.Errorf("prune expired refresh tokens: %w", ctx.Err())
}

func (b *blockingRefreshPruneStore) observedErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func TestRefreshTokenPruner_StopCancelsInFlightDelete(t *testing.T) {
	blocking := &blockingRefreshPruneStore{entered: make(chan struct{})}
	pruner, err := NewRefreshTokenPruner(blocking, time.Millisecond)
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

func TestRefreshTokenPruner_StoreErrorDoesNotKillTheLoop(t *testing.T) {
	fake := &fakeRefreshPruneStore{results: []refreshPruneResult{{err: errors.New("connection refused")}}}
	pruner, err := NewRefreshTokenPruner(fake, 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() >= 2
	}, time.Second, 5*time.Millisecond, "a failed pass must not stop later passes")
}

// Not safe for t.Parallel(); uses global logger
func TestRefreshTokenPruner_CancellationIsNotLoggedAsError(t *testing.T) {
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

			fake := &fakeRefreshPruneStore{results: []refreshPruneResult{{err: tt.err}}}
			pruner := newUnstartedRefreshPruner(fake, time.Hour)
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
func TestRefreshTokenPruner_LogsOnlyWhenRowsDeleted(t *testing.T) {
	t.Run("nothing expired", func(t *testing.T) {
		var buf bytes.Buffer
		original := slog.Default()
		t.Cleanup(func() { slog.SetDefault(original) })
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

		fake := &fakeRefreshPruneStore{results: []refreshPruneResult{{deleted: 0}}}
		pruner := newUnstartedRefreshPruner(fake, time.Hour)

		pruner.prune(pruner.ctx)

		require.Equal(t, 1, fake.callCount())
		assert.Empty(t, buf.String(), "an idle server must not log a no-op line every interval")
	})

	t.Run("rows deleted", func(t *testing.T) {
		var buf bytes.Buffer
		original := slog.Default()
		t.Cleanup(func() { slog.SetDefault(original) })
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

		fake := &fakeRefreshPruneStore{results: []refreshPruneResult{{deleted: 3}}}
		pruner := newUnstartedRefreshPruner(fake, time.Hour)

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
