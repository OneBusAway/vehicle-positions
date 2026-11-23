package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditPruneResult struct {
	deleted int64
	err     error
}

// fakeAuditPruneStore records each cutoff and replays scripted results, one
// per call; once they run out, calls report nothing deleted. It fails the way
// Store.PruneAuditLog does: errors arrive wrapped, and a cancelled context
// fails the call as pgx would. The pruner calls it from its background
// goroutine, so every field is guarded by mu.
type fakeAuditPruneStore struct {
	mu      sync.Mutex
	results []auditPruneResult
	cutoffs []time.Time
}

func (f *fakeAuditPruneStore) PruneAuditLog(ctx context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := len(f.cutoffs)
	f.cutoffs = append(f.cutoffs, cutoff)

	var result auditPruneResult
	if index < len(f.results) {
		result = f.results[index]
	}
	if result.err == nil {
		result.err = ctx.Err()
	}
	if result.err != nil {
		return 0, fmt.Errorf("prune audit log: %w", result.err)
	}
	return result.deleted, nil
}

func (f *fakeAuditPruneStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

// newUnstartedAuditLogPruner builds a pruner without its background
// goroutine, so a single pass can be driven and asserted with no ticker
// firing underneath it. done starts closed because there is no goroutine for
// Stop to wait on.
func newUnstartedAuditLogPruner(store AuditLogPruneStore, retention time.Duration) *AuditLogPruner {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	return &AuditLogPruner{
		store:     store,
		retention: retention,
		interval:  time.Hour,
		ctx:       ctx,
		cancel:    cancel,
		done:      done,
	}
}

// TestStartAuditLogRetention: the log is kept whole unless
// AUDIT_LOG_RETENTION_PERIOD names a positive duration, and a value that
// can't be used is said so in the log, at the level the location retention
// setting uses for it, and keeps everything. Not safe for t.Parallel(): it
// sets the environment and swaps the global logger.
func TestStartAuditLogRetention(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		retention time.Duration
		level     string
		msg       string
	}{
		{name: "unset keeps everything"},
		{name: "zero keeps everything", value: "0"},
		{name: "zero with a unit keeps everything", value: "0s"},
		{name: "a period turns it on", value: "720h", retention: 720 * time.Hour,
			level: "INFO", msg: "audit log retention enabled"},
		{name: "a negative period keeps everything", value: "-24h",
			level: "ERROR", msg: "audit log retention disabled: invalid configuration"},
		// There is no day unit, so 90d is not a duration: envDurationOrDefault
		// warns and falls back to 0, as for every duration setting.
		{name: "a value that is not a duration keeps everything", value: "90d",
			level: "WARN", msg: "invalid duration, using default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AUDIT_LOG_RETENTION_PERIOD", tt.value)
			logs := captureLogs(t)
			fake := &fakeAuditPruneStore{}

			pruner := startAuditLogRetention(fake)

			if tt.retention == 0 {
				assert.Nil(t, pruner, "nothing may be deleted")
			} else {
				require.NotNil(t, pruner)
				t.Cleanup(pruner.Stop)
				assert.Equal(t, tt.retention, pruner.retention)
				assert.Equal(t, auditLogPruneInterval, pruner.interval)
			}

			out := logs.String()
			if tt.msg == "" {
				assert.Empty(t, out, "keeping everything by default is not worth a line")
				return
			}
			assert.Equal(t, 1, strings.Count(out, "\n"), out)
			assert.Contains(t, out, "level="+tt.level)
			assert.Contains(t, out, `msg="`+tt.msg+`"`)
		})
	}
}

func TestNewAuditLogPruner_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name      string
		retention time.Duration
		interval  time.Duration
		wantErr   string
	}{
		{name: "zero retention", retention: 0, interval: time.Hour, wantErr: "audit log retention period must be positive, got 0s"},
		{name: "negative retention", retention: -time.Hour, interval: time.Hour, wantErr: "audit log retention period must be positive, got -1h0m0s"},
		{name: "zero interval", retention: time.Hour, interval: 0, wantErr: "audit log prune interval must be positive, got 0s"},
		{name: "negative interval", retention: time.Hour, interval: -time.Minute, wantErr: "audit log prune interval must be positive, got -1m0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAuditPruneStore{}
			pruner, err := NewAuditLogPruner(fake, tt.retention, tt.interval)

			require.Error(t, err, "invalid configuration must be rejected, not defaulted")
			assert.Equal(t, tt.wantErr, err.Error())
			assert.Nil(t, pruner, "no pruner should be handed back to the caller")
			// A non-positive retention would put the cutoff at "now" or later
			// and delete the whole log, so nothing may run.
			assert.Never(t, func() bool {
				return fake.callCount() > 0
			}, 50*time.Millisecond, 5*time.Millisecond, "rejected configuration must never prune")
		})
	}
}

func TestAuditLogPruner_PrunesOnTick(t *testing.T) {
	fake := &fakeAuditPruneStore{}
	pruner, err := NewAuditLogPruner(fake, time.Hour, 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() > 0
	}, time.Second, 5*time.Millisecond, "pruner should prune on its interval")
}

// TestAuditLogPruner_CutoffIsRetentionBeforeThePass: a later cutoff would
// delete rows the agency asked to keep.
func TestAuditLogPruner_CutoffIsRetentionBeforeThePass(t *testing.T) {
	fake := &fakeAuditPruneStore{}
	pruner := newUnstartedAuditLogPruner(fake, 30*24*time.Hour)

	before := time.Now()
	pruner.prune(pruner.ctx)
	after := time.Now()

	require.Equal(t, 1, fake.callCount())
	cutoff := fake.cutoffs[0]
	assert.False(t, cutoff.Before(before.Add(-30*24*time.Hour)), "cutoff %s is earlier than the retention period allows", cutoff)
	assert.False(t, cutoff.After(after.Add(-30*24*time.Hour)), "cutoff %s would delete rows younger than the retention period", cutoff)
}

func TestAuditLogPruner_StopIsIdempotent(t *testing.T) {
	pruner, err := NewAuditLogPruner(&fakeAuditPruneStore{}, time.Hour, time.Hour)
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

func TestAuditLogPruner_StopWaitsForGoroutineExit(t *testing.T) {
	fake := &fakeAuditPruneStore{}
	pruner, err := NewAuditLogPruner(fake, time.Hour, time.Millisecond)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fake.callCount() > 0
	}, time.Second, time.Millisecond, "pruner should run before it is stopped")

	pruner.Stop()

	// done is closed by run's defer, so a returned Stop proves the goroutine
	// is gone rather than merely signalled.
	select {
	case <-pruner.done:
	default:
		t.Fatal("Stop returned while the pruner goroutine was still running")
	}
	settled := fake.callCount()
	assert.Never(t, func() bool {
		return fake.callCount() > settled
	}, 50*time.Millisecond, 5*time.Millisecond, "a stopped pruner must not prune again")
}

// blockingAuditPruneStore blocks until its context is cancelled, standing in
// for a slow or stuck DELETE on the database, then fails the way the real
// store does.
type blockingAuditPruneStore struct {
	enterOnce sync.Once
	entered   chan struct{}

	mu  sync.Mutex
	err error
}

func (b *blockingAuditPruneStore) PruneAuditLog(ctx context.Context, _ time.Time) (int64, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-ctx.Done()

	b.mu.Lock()
	b.err = ctx.Err()
	b.mu.Unlock()
	return 0, fmt.Errorf("prune audit log: %w", ctx.Err())
}

func (b *blockingAuditPruneStore) observedErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func TestAuditLogPruner_StopCancelsInFlightDelete(t *testing.T) {
	blocking := &blockingAuditPruneStore{entered: make(chan struct{})}
	pruner, err := NewAuditLogPruner(blocking, time.Hour, time.Millisecond)
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

func TestAuditLogPruner_StoreErrorDoesNotKillTheLoop(t *testing.T) {
	fake := &fakeAuditPruneStore{results: []auditPruneResult{{err: errors.New("connection refused")}}}
	pruner, err := NewAuditLogPruner(fake, time.Hour, 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() >= 2
	}, time.Second, 5*time.Millisecond, "a failed pass must not stop later passes")
}

// Not safe for t.Parallel(); uses global logger
func TestAuditLogPruner_CancellationIsNotLoggedAsError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		shutdown  bool
		wantError bool
	}{
		{name: "delete cancelled by Stop", err: context.Canceled, shutdown: true},
		// pgx can surface a cancelled query as a connection error that does
		// not wrap context.Canceled, so the pruner's own context is checked
		// too.
		{name: "other error after Stop", err: errors.New("conn closed"), shutdown: true},
		// The control case: without it, a broken log capture would pass the
		// two above vacuously.
		{name: "genuine failure", err: errors.New("connection refused"), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			fake := &fakeAuditPruneStore{results: []auditPruneResult{{err: tt.err}}}
			pruner := newUnstartedAuditLogPruner(fake, time.Hour)
			if tt.shutdown {
				pruner.Stop()
			}

			pruner.prune(pruner.ctx)

			require.Equal(t, 1, fake.callCount())
			out := logs.String()
			if !tt.wantError {
				assert.Empty(t, out, "a delete aborted by shutdown is expected and must not be logged")
				return
			}
			assert.Contains(t, out, "level=ERROR", "a genuine failure must be logged")
			assert.Contains(t, out, `msg="audit log retention prune failed"`)
			assert.Contains(t, out, "connection refused")
		})
	}
}

// Not safe for t.Parallel(); uses global logger
func TestAuditLogPruner_LogsOnlyWhenRowsDeleted(t *testing.T) {
	t.Run("nothing old enough", func(t *testing.T) {
		logs := captureLogs(t)
		fake := &fakeAuditPruneStore{results: []auditPruneResult{{deleted: 0}}}
		pruner := newUnstartedAuditLogPruner(fake, time.Hour)

		pruner.prune(pruner.ctx)

		require.Equal(t, 1, fake.callCount())
		assert.Empty(t, logs.String(), "an idle server must not log a no-op line every hour")
	})

	t.Run("rows deleted", func(t *testing.T) {
		logs := captureLogs(t)
		fake := &fakeAuditPruneStore{results: []auditPruneResult{{deleted: 3}}}
		pruner := newUnstartedAuditLogPruner(fake, time.Hour)

		pruner.prune(pruner.ctx)

		out := logs.String()
		assert.Contains(t, out, "level=INFO")
		assert.Contains(t, out, `msg="pruned expired audit log rows"`)
		assert.Contains(t, out, " deleted=3 ")
		assert.Contains(t, out, " retention=1h0m0s ")
		assert.Regexp(t, `duration_ms=[0-9.e-]+\n$`, out,
			"duration must be numeric so log tooling can filter and sort on it")
	})
}
