package main

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTripTestData creates a user, a vehicle, and assigns them for trip tests.
// Returns the user ID.
func setupTripTestData(t *testing.T, store *Store) int64 {
	t.Helper()
	ctx := context.Background()

	// Clean up in correct order (respect FK constraints).
	_, err := store.pool.Exec(ctx, "DELETE FROM trips")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM user_vehicles")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM users")
	require.NoError(t, err)

	// Create a test user.
	var userID int64
	err = store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Trip Driver', 'tripdriver@test.com', '$2a$10$dummyhash000000000000000000000000000000000000000000', 'driver')
		 RETURNING id`,
	).Scan(&userID)
	require.NoError(t, err)

	// Create a test vehicle.
	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id, label) VALUES ('bus-trip-1', 'Bus 1')")
	require.NoError(t, err)

	// Assign user to vehicle.
	_, err = store.pool.Exec(ctx, "INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, 'bus-trip-1')", userID)
	require.NoError(t, err)

	return userID
}

func TestStore_StartTrip_Success(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "route_5_0830")
	require.NoError(t, err)

	assert.Equal(t, userID, trip.UserID)
	assert.Equal(t, "bus-trip-1", trip.VehicleID)
	assert.Equal(t, "route-5", trip.RouteID)
	assert.Equal(t, "route_5_0830", trip.GtfsTripID)
	assert.Equal(t, "active", trip.Status)
	assert.NotZero(t, trip.ID)
	assert.NotZero(t, trip.StartTime)
	assert.Nil(t, trip.EndTime)
}

func TestStore_StartTrip_NotAssigned(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	_, err := store.StartTrip(ctx, userID, "bus-not-assigned", "route-5", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotAssigned)
}

func TestStore_StartTrip_AlreadyActive(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	// Start first trip.
	_, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	// Second trip should fail.
	_, err = store.StartTrip(ctx, userID, "bus-trip-1", "route-6", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrActiveTripExists)
}

func TestStore_StartTrip_RollbackOnDuplicate(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	// Start first trip.
	_, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	// Attempt second trip — should fail.
	_, err = store.StartTrip(ctx, userID, "bus-trip-1", "route-6", "")
	require.Error(t, err)

	// Verify only one trip exists (no stale rows from rolled-back attempt).
	var count int
	err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM trips WHERE user_id = $1", userID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "rolled-back trip should not leave stale rows")
}

func TestStore_EndTrip_Success(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)

	// Verify trip is completed in DB.
	var status string
	err = store.pool.QueryRow(ctx, "SELECT status FROM trips WHERE id = $1", trip.ID).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)

	// Verify end_time is set.
	var endTimeSet bool
	err = store.pool.QueryRow(ctx, "SELECT end_time IS NOT NULL FROM trips WHERE id = $1", trip.ID).Scan(&endTimeSet)
	require.NoError(t, err)
	assert.True(t, endTimeSet, "end_time should be set after ending trip")
}

func TestStore_EndTrip_NotFound(t *testing.T) {
	store := newTestStore(t)
	_ = setupTripTestData(t, store)
	ctx := context.Background()

	err := store.EndTrip(ctx, 99999, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTripNotFound)
}

func TestStore_EndTrip_WrongUser(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	// Try to end with a different user ID.
	err = store.EndTrip(ctx, trip.ID, userID+999)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTripNotFound, "should not allow ending another user's trip")
}

func TestStore_EndTrip_AlreadyEnded(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	// End once.
	err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)

	// End again — should fail.
	err = store.EndTrip(ctx, trip.ID, userID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTripNotFound, "ending an already-completed trip should return not found")
}

func TestStore_StartTrip_AfterEndingPrevious(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	// Start and end a trip.
	trip1, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)
	err = store.EndTrip(ctx, trip1.ID, userID)
	require.NoError(t, err)

	// Should be able to start a new trip.
	trip2, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-6", "")
	require.NoError(t, err)
	assert.NotEqual(t, trip1.ID, trip2.ID)
	assert.Equal(t, "active", trip2.Status)
}

func TestStore_StartTrip_ConcurrentAttempts(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	successes := make(chan int64, goroutines)
	failures := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			trip, err := store.StartTrip(context.Background(), userID, "bus-trip-1", "route-5", "")
			if err != nil {
				failures <- err
				return
			}
			successes <- trip.ID
		}()
	}

	wg.Wait()
	close(successes)
	close(failures)

	// Exactly one goroutine should succeed.
	var successCount int
	for range successes {
		successCount++
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent StartTrip should succeed")

	// The rest should fail with ErrActiveTripExists or ErrNotAssigned (serialization error).
	var failCount int
	for range failures {
		failCount++
	}
	assert.Equal(t, goroutines-1, failCount)

	// Verify only one trip in DB.
	var count int
	err := store.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM trips WHERE user_id = $1 AND status = 'active'", userID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "only one active trip should exist after concurrent attempts")
}

func TestStore_StartTrip_EmptyOptionalFields(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	// route_id and gtfs_trip_id are optional — empty strings should work.
	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "", "")
	require.NoError(t, err)
	assert.Equal(t, "", trip.RouteID)
	assert.Equal(t, "", trip.GtfsTripID)
}

func TestStore_EndTrip_UpdatedAtChanges(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	// Get created_at for comparison.
	var createdAt, updatedAt interface{}
	err = store.pool.QueryRow(ctx, "SELECT created_at, updated_at FROM trips WHERE id = $1", trip.ID).Scan(&createdAt, &updatedAt)
	require.NoError(t, err)

	err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)

	var newUpdatedAt interface{}
	err = store.pool.QueryRow(ctx, "SELECT updated_at FROM trips WHERE id = $1", trip.ID).Scan(&newUpdatedAt)
	require.NoError(t, err)
	assert.NotEqual(t, updatedAt, newUpdatedAt, "updated_at should change after ending trip")
}
