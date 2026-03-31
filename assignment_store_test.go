package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestUserForAssignment inserts a user and returns the generated ID.
func createTestUserForAssignment(t *testing.T, store *Store, email string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role) VALUES ($1, $2, $3, $4) RETURNING id`,
		"Test User", email,
		"$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		"driver",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// createTestVehicleForAssignment inserts a vehicle.
func createTestVehicleForAssignment(t *testing.T, store *Store, vehicleID string) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `INSERT INTO vehicles (id) VALUES ($1) ON CONFLICT (id) DO NOTHING`, vehicleID)
	require.NoError(t, err)
}

// cleanupAssignmentTestData removes all test data in correct FK order.
func cleanupAssignmentTestData(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, "DELETE FROM user_vehicles")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM users")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)
}

func TestStore_CreateAssignment_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "roundtrip@test.com")
	createTestVehicleForAssignment(t, store, "bus-rt-1")

	assignment, err := store.CreateAssignment(ctx, userID, "bus-rt-1")
	require.NoError(t, err)
	assert.Equal(t, userID, assignment.UserID)
	assert.Equal(t, "bus-rt-1", assignment.VehicleID)
	assert.WithinDuration(t, time.Now(), assignment.CreatedAt, 5*time.Second)

	// Verify via list by user
	byUser, err := store.ListAssignmentsByUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, byUser, 1)
	assert.Equal(t, userID, byUser[0].UserID)
	assert.Equal(t, "bus-rt-1", byUser[0].VehicleID)
	assert.Equal(t, assignment.CreatedAt.Unix(), byUser[0].CreatedAt.Unix())

	// Verify via list by vehicle
	byVehicle, err := store.ListAssignmentsByVehicle(ctx, "bus-rt-1")
	require.NoError(t, err)
	require.Len(t, byVehicle, 1)
	assert.Equal(t, userID, byVehicle[0].UserID)
}

func TestStore_CreateAssignment_Duplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "dup@test.com")
	createTestVehicleForAssignment(t, store, "bus-dup")

	_, err := store.CreateAssignment(ctx, userID, "bus-dup")
	require.NoError(t, err)

	_, err = store.CreateAssignment(ctx, userID, "bus-dup")
	assert.ErrorIs(t, err, ErrAssignmentExists)

	// Verify only one row exists (no partial insert)
	var count int
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM user_vehicles WHERE user_id = $1 AND vehicle_id = $2",
		userID, "bus-dup",
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestStore_DeleteAssignment_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "del@test.com")
	createTestVehicleForAssignment(t, store, "bus-del")

	_, err := store.CreateAssignment(ctx, userID, "bus-del")
	require.NoError(t, err)

	err = store.DeleteAssignment(ctx, userID, "bus-del")
	require.NoError(t, err)

	// Verify it's gone
	byUser, err := store.ListAssignmentsByUser(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, byUser)
}

func TestStore_DeleteAssignment_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.DeleteAssignment(ctx, 99999, "nonexistent")
	assert.ErrorIs(t, err, ErrAssignmentNotFound)
}

func TestStore_CreateAssignment_UserFKViolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	createTestVehicleForAssignment(t, store, "bus-fk-user")

	_, err := store.CreateAssignment(ctx, 99999, "bus-fk-user")
	assert.ErrorIs(t, err, ErrUserNotFoundFK)

	// Rollback verification: no stale rows
	var count int
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM user_vehicles WHERE user_id = $1", int64(99999),
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "no stale rows should exist after FK violation")
}

func TestStore_CreateAssignment_VehicleFKViolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "fk-vehicle@test.com")

	_, err := store.CreateAssignment(ctx, userID, "nonexistent-vehicle")
	assert.ErrorIs(t, err, ErrVehicleNotFoundFK)

	// Rollback verification: no stale rows
	var count int
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM user_vehicles WHERE vehicle_id = $1", "nonexistent-vehicle",
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "no stale rows should exist after FK violation")
}

func TestStore_ListAssignmentsByUser_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	assignments, err := store.ListAssignmentsByUser(ctx, 99999)
	require.NoError(t, err)
	assert.NotNil(t, assignments, "must return non-nil slice")
	assert.Empty(t, assignments)
}

func TestStore_ListAssignmentsByVehicle_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	assignments, err := store.ListAssignmentsByVehicle(ctx, "nonexistent")
	require.NoError(t, err)
	assert.NotNil(t, assignments, "must return non-nil slice")
	assert.Empty(t, assignments)
}

func TestStore_ListAssignmentsByUser_MultipleVehicles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "multi@test.com")
	createTestVehicleForAssignment(t, store, "bus-m1")
	createTestVehicleForAssignment(t, store, "bus-m2")
	createTestVehicleForAssignment(t, store, "bus-m3")

	// Insert with explicit created_at offsets to guarantee ordering without time.Sleep
	now := time.Now()
	_, err := store.pool.Exec(ctx,
		"INSERT INTO user_vehicles (user_id, vehicle_id, created_at) VALUES ($1, $2, $3)",
		userID, "bus-m1", now.Add(-2*time.Second))
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx,
		"INSERT INTO user_vehicles (user_id, vehicle_id, created_at) VALUES ($1, $2, $3)",
		userID, "bus-m2", now.Add(-1*time.Second))
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx,
		"INSERT INTO user_vehicles (user_id, vehicle_id, created_at) VALUES ($1, $2, $3)",
		userID, "bus-m3", now)
	require.NoError(t, err)

	assignments, err := store.ListAssignmentsByUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, assignments, 3)

	// Ordered by created_at DESC — most recent first
	assert.Equal(t, "bus-m3", assignments[0].VehicleID)
	assert.Equal(t, "bus-m2", assignments[1].VehicleID)
	assert.Equal(t, "bus-m1", assignments[2].VehicleID)
}

func TestStore_ListAssignmentsByVehicle_MultipleUsers(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	user1 := createTestUserForAssignment(t, store, "user1@test.com")
	user2 := createTestUserForAssignment(t, store, "user2@test.com")
	user3 := createTestUserForAssignment(t, store, "user3@test.com")
	createTestVehicleForAssignment(t, store, "bus-shared")

	// Insert with explicit created_at offsets to guarantee ordering without time.Sleep
	now := time.Now()
	_, err := store.pool.Exec(ctx,
		"INSERT INTO user_vehicles (user_id, vehicle_id, created_at) VALUES ($1, $2, $3)",
		user1, "bus-shared", now.Add(-2*time.Second))
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx,
		"INSERT INTO user_vehicles (user_id, vehicle_id, created_at) VALUES ($1, $2, $3)",
		user2, "bus-shared", now.Add(-1*time.Second))
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx,
		"INSERT INTO user_vehicles (user_id, vehicle_id, created_at) VALUES ($1, $2, $3)",
		user3, "bus-shared", now)
	require.NoError(t, err)

	assignments, err := store.ListAssignmentsByVehicle(ctx, "bus-shared")
	require.NoError(t, err)
	require.Len(t, assignments, 3)

	// Ordered by created_at DESC — most recent first
	assert.Equal(t, user3, assignments[0].UserID)
	assert.Equal(t, user2, assignments[1].UserID)
	assert.Equal(t, user1, assignments[2].UserID)
}

func TestStore_CascadeDeleteUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "cascade-user@test.com")
	createTestVehicleForAssignment(t, store, "bus-cascade-u")

	_, err := store.CreateAssignment(ctx, userID, "bus-cascade-u")
	require.NoError(t, err)

	// Delete the user — CASCADE should remove assignment
	_, err = store.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	require.NoError(t, err)

	// Verify assignment is gone
	var count int
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM user_vehicles WHERE user_id = $1", userID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "ON DELETE CASCADE should remove assignment when user is deleted")
}

func TestStore_CascadeDeleteVehicle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanupAssignmentTestData(t, store) })
	cleanupAssignmentTestData(t, store)

	userID := createTestUserForAssignment(t, store, "cascade-vehicle@test.com")
	createTestVehicleForAssignment(t, store, "bus-cascade-v")

	_, err := store.CreateAssignment(ctx, userID, "bus-cascade-v")
	require.NoError(t, err)

	// Delete the vehicle — CASCADE should remove assignment
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles WHERE id = $1", "bus-cascade-v")
	require.NoError(t, err)

	// Verify assignment is gone
	var count int
	err = store.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM user_vehicles WHERE vehicle_id = $1", "bus-cascade-v",
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "ON DELETE CASCADE should remove assignment when vehicle is deleted")
}
