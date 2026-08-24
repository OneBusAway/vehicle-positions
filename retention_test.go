package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pruneCall struct {
	cutoff    time.Time
	batchSize int32
}

// fakeLocationPruneStore records prune calls and replays a scripted sequence of
// deletion counts. The pruner calls it from its background goroutine, so every
// field is guarded by mu.
type fakeLocationPruneStore struct {
	mu sync.Mutex
	// deleted holds the row count returned per call; the final entry repeats
	// for any further calls.
	deleted []int64
	err     error
	onCall  func()
	calls   []pruneCall
}

func (f *fakeLocationPruneStore) PruneLocationPoints(_ context.Context, cutoff time.Time, batchSize int32) (int64, error) {
	f.mu.Lock()
	index := len(f.calls)
	f.calls = append(f.calls, pruneCall{cutoff: cutoff, batchSize: batchSize})
	deleted, err, onCall := f.deleted, f.err, f.onCall
	f.mu.Unlock()

	if onCall != nil {
		onCall()
	}
	if err != nil {
		return 0, err
	}
	if len(deleted) == 0 {
		return 0, nil
	}
	if index >= len(deleted) {
		index = len(deleted) - 1
	}
	return deleted[index], nil
}

func (f *fakeLocationPruneStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeLocationPruneStore) firstCall(t *testing.T) pruneCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "store was never called")
	return f.calls[0]
}

// newUnstartedPruner builds a pruner without its background goroutine, so the
// batch loop in pruneAll can be asserted exactly with no ticker firing extra
// passes underneath the assertions.
func newUnstartedPruner(store LocationPruneStore, retention time.Duration, batchSize int32) *LocationPruner {
	return &LocationPruner{
		store:     store,
		retention: retention,
		batchSize: batchSize,
		stop:      make(chan struct{}),
	}
}

func TestLocationPruner_PrunesOnTick(t *testing.T) {
	fake := &fakeLocationPruneStore{}
	pruner := NewLocationPruner(fake, time.Hour, 5*time.Millisecond, 250)
	t.Cleanup(pruner.Stop)

	assert.Eventually(t, func() bool {
		return fake.callCount() > 0
	}, time.Second, 5*time.Millisecond, "pruner should prune on its interval")

	assert.Equal(t, int32(250), fake.firstCall(t).batchSize, "configured batch size should reach the store")
}

func TestLocationPruner_LoopsUntilUnderBatch(t *testing.T) {
	fake := &fakeLocationPruneStore{deleted: []int64{5, 5, 1}}
	pruner := newUnstartedPruner(fake, time.Hour, 5)

	pruner.pruneAll(context.Background())

	assert.Equal(t, 3, fake.callCount(), "should keep draining until a batch comes back short")
}

func TestLocationPruner_StopsOnError(t *testing.T) {
	fake := &fakeLocationPruneStore{deleted: []int64{5}, err: errors.New("connection refused")}
	pruner := newUnstartedPruner(fake, time.Hour, 5)

	pruner.pruneAll(context.Background())

	assert.Equal(t, 1, fake.callCount(), "a store error should end the pass instead of retrying in a tight loop")
}

func TestLocationPruner_CutoffUsesRetention(t *testing.T) {
	fake := &fakeLocationPruneStore{}
	pruner := newUnstartedPruner(fake, 24*time.Hour, 100)

	before := time.Now()
	pruner.pruneAll(context.Background())

	assert.WithinDuration(t, before.Add(-24*time.Hour), fake.firstCall(t).cutoff, time.Minute,
		"cutoff should be one retention period in the past")
}

func TestLocationPruner_Stop_Idempotent(t *testing.T) {
	pruner := NewLocationPruner(&fakeLocationPruneStore{}, time.Hour, time.Minute, 100)

	assert.NotPanics(t, func() {
		pruner.Stop()
		pruner.Stop()
	}, "Stop is guarded by sync.Once and must tolerate repeat calls")
}

func TestLocationPruner_Stop_HaltsGoroutine(t *testing.T) {
	fake := &fakeLocationPruneStore{}
	pruner := NewLocationPruner(fake, time.Hour, 5*time.Millisecond, 100)
	t.Cleanup(pruner.Stop)

	require.Eventually(t, func() bool {
		return fake.callCount() > 0
	}, time.Second, 5*time.Millisecond, "pruner should run at least once before being stopped")

	pruner.Stop()
	settled := fake.callCount()

	// The interval is 5ms, so a goroutine still running would add many calls in
	// this window. Tolerate one pass that was already in flight when Stop landed.
	assert.Never(t, func() bool {
		return fake.callCount() > settled+1
	}, 100*time.Millisecond, 10*time.Millisecond, "stopped pruner should not keep ticking")
}

func TestLocationPruner_StopDuringBatchLoop(t *testing.T) {
	// Every call reports a full batch, so the loop would drain forever if Stop
	// did not break it.
	fake := &fakeLocationPruneStore{deleted: []int64{5}}
	pruner := newUnstartedPruner(fake, time.Hour, 5)
	fake.onCall = pruner.Stop

	pruner.pruneAll(context.Background())

	assert.Equal(t, 1, fake.callCount(), "Stop should break the batch loop instead of draining the backlog")
}

func TestNewLocationPruner_RefusesInvalidConfig(t *testing.T) {
	// Not safe for t.Parallel(); uses global logger
	tests := []struct {
		name      string
		retention time.Duration
		interval  time.Duration
		batchSize int32
	}{
		{name: "zero retention", retention: 0, interval: 5 * time.Millisecond, batchSize: 100},
		{name: "negative retention", retention: -time.Hour, interval: 5 * time.Millisecond, batchSize: 100},
		{name: "zero interval", retention: time.Hour, interval: 0, batchSize: 100},
		{name: "negative interval", retention: time.Hour, interval: -time.Second, batchSize: 100},
		{name: "zero batch size", retention: time.Hour, interval: 5 * time.Millisecond, batchSize: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeLocationPruneStore{}
			pruner := NewLocationPruner(fake, tt.retention, tt.interval, tt.batchSize)
			t.Cleanup(pruner.Stop)

			// A zero retention would set the cutoff at "now" and delete the
			// entire table, so the pruner must never run with one.
			assert.Never(t, func() bool {
				return fake.callCount() > 0
			}, 50*time.Millisecond, 5*time.Millisecond, "pruner must not run with invalid configuration")
		})
	}
}
