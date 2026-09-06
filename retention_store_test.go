package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertLocationPointAt writes a point with an explicit received_at. The sqlc
// insert always takes NOW() for that column, so retention tests set it directly.
func insertLocationPointAt(t *testing.T, store *Store, vehicleID string, receivedAt time.Time) {
	t.Helper()
	_, err := store.pool.Exec(context.Background(),
		"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ($1, '', $2, $3, $4, $5)",
		vehicleID, -1.29, 36.82, time.Now().Unix(), receivedAt)
	require.NoError(t, err)
}

func countLocationPoints(t *testing.T, store *Store) int64 {
	t.Helper()
	var count int64
	err := store.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM location_points").Scan(&count)
	require.NoError(t, err)
	return count
}

// setupRetentionTest clears location data and creates the vehicle that the
// retention tests attach their points to.
func setupRetentionTest(t *testing.T, store *Store, vehicleID string) {
	t.Helper()
	clearVehicleData(t, store)
	_, err := store.pool.Exec(context.Background(), "INSERT INTO vehicles (id) VALUES ($1)", vehicleID)
	require.NoError(t, err)
}

func TestStore_PruneLocationPoints_DeletesExpired(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	setupRetentionTest(t, store, "prune-bus-expired")

	now := time.Now()
	for _, age := range []time.Duration{2 * time.Hour, 3 * time.Hour, 25 * time.Hour} {
		insertLocationPointAt(t, store, "prune-bus-expired", now.Add(-age))
	}
	require.Equal(t, int64(3), countLocationPoints(t, store))

	deleted, err := store.PruneLocationPoints(ctx, now.Add(-time.Hour), 100)
	require.NoError(t, err)

	assert.Equal(t, int64(3), deleted)
	assert.Equal(t, int64(0), countLocationPoints(t, store))
}

func TestStore_PruneLocationPoints_PreservesRecent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	setupRetentionTest(t, store, "prune-bus-recent")

	now := time.Now()
	insertLocationPointAt(t, store, "prune-bus-recent", now.Add(-48*time.Hour))
	insertLocationPointAt(t, store, "prune-bus-recent", now.Add(-25*time.Hour))
	insertLocationPointAt(t, store, "prune-bus-recent", now.Add(-30*time.Minute))
	insertLocationPointAt(t, store, "prune-bus-recent", now)

	cutoff := now.Add(-24 * time.Hour)
	deleted, err := store.PruneLocationPoints(ctx, cutoff, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	var survivorsOlderThanCutoff int64
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM location_points WHERE received_at < $1", cutoff).Scan(&survivorsOlderThanCutoff)
	require.NoError(t, err)

	assert.Equal(t, int64(2), countLocationPoints(t, store), "points inside the retention window must survive")
	assert.Equal(t, int64(0), survivorsOlderThanCutoff, "no expired point should remain")
}

func TestStore_PruneLocationPoints_RespectsBatchSize(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	setupRetentionTest(t, store, "prune-bus-batch")

	const batchSize = 3
	expired := time.Now().Add(-2 * time.Hour)
	for i := 0; i < batchSize+2; i++ {
		insertLocationPointAt(t, store, "prune-bus-batch", expired)
	}

	deleted, err := store.PruneLocationPoints(ctx, time.Now().Add(-time.Hour), batchSize)
	require.NoError(t, err)

	assert.Equal(t, int64(batchSize), deleted, "a single call must not exceed the batch size")
	assert.Equal(t, int64(2), countLocationPoints(t, store), "the remainder is left for the next batch")
}

func TestStore_PruneLocationPoints_ReturnsCount(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	setupRetentionTest(t, store, "prune-bus-count")

	now := time.Now()
	for i := 0; i < 4; i++ {
		insertLocationPointAt(t, store, "prune-bus-count", now.Add(-2*time.Hour))
	}
	insertLocationPointAt(t, store, "prune-bus-count", now)

	before := countLocationPoints(t, store)
	deleted, err := store.PruneLocationPoints(ctx, now.Add(-time.Hour), 100)
	require.NoError(t, err)
	after := countLocationPoints(t, store)

	assert.Equal(t, before-after, deleted, "reported count must match rows actually removed")
	assert.Equal(t, int64(4), deleted)
}

func TestStore_PruneLocationPoints_EmptyTable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	setupRetentionTest(t, store, "prune-bus-empty")
	require.Equal(t, int64(0), countLocationPoints(t, store))

	deleted, err := store.PruneLocationPoints(ctx, time.Now(), 100)

	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

func TestStore_PruneLocationPoints_BoundaryExact(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	setupRetentionTest(t, store, "prune-bus-boundary")

	// Postgres stores timestamptz at microsecond precision, so truncating here
	// keeps the boundary row exactly equal to the cutoff after the round trip.
	cutoff := time.Now().Truncate(time.Microsecond).Add(-time.Hour)
	insertLocationPointAt(t, store, "prune-bus-boundary", cutoff)
	insertLocationPointAt(t, store, "prune-bus-boundary", cutoff.Add(-time.Microsecond))

	deleted, err := store.PruneLocationPoints(ctx, cutoff, 100)
	require.NoError(t, err)

	assert.Equal(t, int64(1), deleted, "the predicate is < cutoff, so only the older row goes")
	require.Equal(t, int64(1), countLocationPoints(t, store))

	var survivor time.Time
	err = store.pool.QueryRow(ctx, "SELECT received_at FROM location_points").Scan(&survivor)
	require.NoError(t, err)
	assert.True(t, survivor.Equal(cutoff), "the row exactly at the cutoff must be retained")
}
