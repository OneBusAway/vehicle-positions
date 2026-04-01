package main

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupVehicles ensures a clean slate before and after each test.
// Pre-test cleanup handles cases where a prior test panicked before its t.Cleanup ran.
func cleanupVehicles(t *testing.T, store *Store) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
		require.NoError(t, err)
		_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
		require.NoError(t, err)
	})
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)
}

func TestStore_UpsertVehicle_CreateNew(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	v, err := store.UpsertVehicle(context.Background(), "bus-42", "Bus 42", "nairobi")
	require.NoError(t, err)

	assert.Equal(t, "bus-42", v.ID)
	assert.Equal(t, "Bus 42", v.Label)
	assert.Equal(t, "nairobi", v.AgencyTag)
	assert.True(t, v.Active)
	assert.False(t, v.CreatedAt.IsZero())
	assert.False(t, v.UpdatedAt.IsZero())
}

func TestStore_UpsertVehicle_UpdateExisting(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	created, err := store.UpsertVehicle(ctx, "bus-upd", "Old Label", "old-tag")
	require.NoError(t, err)

	updated, err := store.UpsertVehicle(ctx, "bus-upd", "New Label", "new-tag")
	require.NoError(t, err)

	assert.Equal(t, "bus-upd", updated.ID)
	assert.Equal(t, "New Label", updated.Label)
	assert.Equal(t, "new-tag", updated.AgencyTag)
	assert.True(t, updated.Active)
	assert.Equal(t, created.CreatedAt, updated.CreatedAt, "created_at should not change on upsert update")
	assert.True(t, updated.UpdatedAt.After(created.UpdatedAt) || updated.UpdatedAt.Equal(created.UpdatedAt),
		"updated_at should be >= created_at after upsert update")
}

func TestStore_ListVehicles(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-a", "Bus A", "agency-1")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "bus-b", "Bus B", "agency-2")
	require.NoError(t, err)

	vehicles, err := store.ListVehicles(ctx)
	require.NoError(t, err)
	assert.Len(t, vehicles, 2)
}

func TestStore_ListVehicles_Empty(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	vehicles, err := store.ListVehicles(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, vehicles, "empty list should be [], not nil")
	assert.Empty(t, vehicles)
}

func TestStore_GetVehicle(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-get", "Bus Get", "nairobi")
	require.NoError(t, err)

	v, err := store.GetVehicle(ctx, "bus-get")
	require.NoError(t, err)
	assert.Equal(t, "bus-get", v.ID)
	assert.Equal(t, "Bus Get", v.Label)
	assert.Equal(t, "nairobi", v.AgencyTag)
	assert.True(t, v.Active)
}

func TestStore_GetVehicle_NotFound(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	v, err := store.GetVehicle(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, v)
	assert.True(t, errors.Is(err, pgx.ErrNoRows),
		"error must wrap pgx.ErrNoRows so handlers can distinguish not-found from database failures")
}

func TestStore_DeactivateVehicle(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-deact", "Bus Deact", "")
	require.NoError(t, err)

	err = store.DeactivateVehicle(ctx, "bus-deact")
	require.NoError(t, err)

	v, err := store.GetVehicle(ctx, "bus-deact")
	require.NoError(t, err)
	assert.False(t, v.Active, "vehicle should be inactive after deactivation")
}

func TestStore_DeactivateVehicle_NotFound(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	err := store.DeactivateVehicle(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pgx.ErrNoRows),
		"error must wrap pgx.ErrNoRows so handlers can distinguish not-found from database failures")
}

func TestStore_DeactivateVehicle_AlreadyInactive(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-idem", "Bus Idem", "")
	require.NoError(t, err)

	err = store.DeactivateVehicle(ctx, "bus-idem")
	require.NoError(t, err)

	// The SQL matches on id only (not active), so deactivating an already-inactive
	// vehicle still affects one row and succeeds without error.
	err = store.DeactivateVehicle(ctx, "bus-idem")
	require.NoError(t, err, "deactivating an already-inactive vehicle should not error")
}

func TestStore_UpsertVehicle_ReactivatesDeactivatedVehicle(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-react", "Bus React", "agency-1")
	require.NoError(t, err)

	err = store.DeactivateVehicle(ctx, "bus-react")
	require.NoError(t, err)

	v, err := store.GetVehicle(ctx, "bus-react")
	require.NoError(t, err)
	require.False(t, v.Active, "vehicle should be inactive after deactivation")

	// Upserting a deactivated vehicle should reactivate it.
	reactivated, err := store.UpsertVehicle(ctx, "bus-react", "Bus React Updated", "agency-2")
	require.NoError(t, err)
	assert.True(t, reactivated.Active, "upsert should reactivate a deactivated vehicle")
	assert.Equal(t, "Bus React Updated", reactivated.Label)
	assert.Equal(t, "agency-2", reactivated.AgencyTag)
}

func TestStore_UpsertVehicle_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	created, err := store.UpsertVehicle(ctx, "bus-rt", "Bus RT", "agency-rt")
	require.NoError(t, err)

	fetched, err := store.GetVehicle(ctx, "bus-rt")
	require.NoError(t, err)

	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, created.Label, fetched.Label)
	assert.Equal(t, created.AgencyTag, fetched.AgencyTag)
	assert.Equal(t, created.Active, fetched.Active)
	assert.Equal(t, created.CreatedAt.Unix(), fetched.CreatedAt.Unix(), "created_at should round-trip")
	assert.Equal(t, created.UpdatedAt.Unix(), fetched.UpdatedAt.Unix(), "updated_at should round-trip")
}
