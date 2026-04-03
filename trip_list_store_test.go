package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTripListTestData creates users, vehicles, assignments, and trips for list tests.
// Returns (userID1, userID2).
func setupTripListTestData(t *testing.T, store *Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()

	// Clean up in FK-safe order: location_points → trips → user_vehicles → users → vehicles.
	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM trips")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM user_vehicles")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM users")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	// Create two test users.
	var userID1, userID2 int64
	err = store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Driver A', 'drivera@test.com', '$2a$10$dummyhash000000000000000000000000000000000000000000', 'driver')
		 RETURNING id`,
	).Scan(&userID1)
	require.NoError(t, err)

	err = store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Driver B', 'driverb@test.com', '$2a$10$dummyhash000000000000000000000000000000000000000000', 'driver')
		 RETURNING id`,
	).Scan(&userID2)
	require.NoError(t, err)

	// Create two test vehicles.
	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id, label) VALUES ('bus-list-1', 'Bus 1')")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id, label) VALUES ('bus-list-2', 'Bus 2')")
	require.NoError(t, err)

	// Assign users to vehicles.
	_, err = store.pool.Exec(ctx, "INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, 'bus-list-1')", userID1)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, 'bus-list-2')", userID2)
	require.NoError(t, err)

	return userID1, userID2
}

// insertTrip inserts a trip directly for test setup.
// Note: the DB enforces one active trip per user (idx_trips_one_active_per_user).
// Only insert one "active" trip per user_id, or use "completed" status.
func insertTrip(t *testing.T, store *Store, userID int64, vehicleID, routeID, status string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := store.pool.QueryRow(ctx,
		`INSERT INTO trips (user_id, vehicle_id, route_id, gtfs_trip_id, status)
		 VALUES ($1, $2, $3, '', $4) RETURNING id`,
		userID, vehicleID, routeID, status,
	).Scan(&id)
	require.NoError(t, err)

	// If completed, set end_time.
	if status == "completed" {
		_, err = store.pool.Exec(ctx, "UPDATE trips SET end_time = NOW() WHERE id = $1", id)
		require.NoError(t, err)
	}
	return id
}

func TestListTrips_Success(t *testing.T) {
	store := newTestStore(t)
	userID1, _ := setupTripListTestData(t, store)
	ctx := context.Background()

	// Insert trips with different start times.
	insertTrip(t, store, userID1, "bus-list-1", "route-1", "completed")
	time.Sleep(time.Millisecond)
	insertTrip(t, store, userID1, "bus-list-1", "route-2", "active")

	trips, err := store.ListTrips(ctx, nil, nil, nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, trips, 2)

	// Verify DESC order by start_time (most recent first).
	assert.True(t, trips[0].StartTime.After(trips[1].StartTime) || trips[0].StartTime.Equal(trips[1].StartTime),
		"trips should be ordered by start_time DESC")
}

func TestListTrips_FilterByStatus(t *testing.T) {
	store := newTestStore(t)
	userID1, _ := setupTripListTestData(t, store)
	ctx := context.Background()

	insertTrip(t, store, userID1, "bus-list-1", "route-1", "completed")
	// Need to end the active trip constraint before inserting another active.
	insertTrip(t, store, userID1, "bus-list-1", "route-2", "completed")

	active := "active"
	completed := "completed"

	trips, err := store.ListTrips(ctx, &completed, nil, nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, trips, 2)
	for _, trip := range trips {
		assert.Equal(t, "completed", trip.Status)
	}

	trips, err = store.ListTrips(ctx, &active, nil, nil, 50, 0)
	require.NoError(t, err)
	assert.Len(t, trips, 0)
}

func TestListTrips_FilterByVehicle(t *testing.T) {
	store := newTestStore(t)
	userID1, userID2 := setupTripListTestData(t, store)
	ctx := context.Background()

	insertTrip(t, store, userID1, "bus-list-1", "route-1", "completed")
	insertTrip(t, store, userID2, "bus-list-2", "route-2", "completed")

	vid := "bus-list-1"
	trips, err := store.ListTrips(ctx, nil, &vid, nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, trips, 1)
	assert.Equal(t, "bus-list-1", trips[0].VehicleID)
}

func TestListTrips_FilterByUserID(t *testing.T) {
	store := newTestStore(t)
	userID1, userID2 := setupTripListTestData(t, store)
	ctx := context.Background()

	insertTrip(t, store, userID1, "bus-list-1", "route-1", "completed")
	insertTrip(t, store, userID2, "bus-list-2", "route-2", "completed")

	trips, err := store.ListTrips(ctx, nil, nil, &userID2, 50, 0)
	require.NoError(t, err)
	require.Len(t, trips, 1)
	assert.Equal(t, userID2, trips[0].UserID)
}

func TestListTrips_Pagination(t *testing.T) {
	store := newTestStore(t)
	userID1, _ := setupTripListTestData(t, store)
	ctx := context.Background()

	// Insert 5 completed trips.
	for i := 0; i < 5; i++ {
		insertTrip(t, store, userID1, "bus-list-1", "route-1", "completed")
	}

	// Page 1: limit=2, offset=0.
	trips, err := store.ListTrips(ctx, nil, nil, nil, 2, 0)
	require.NoError(t, err)
	assert.Len(t, trips, 2)

	// Page 2: limit=2, offset=2.
	trips, err = store.ListTrips(ctx, nil, nil, nil, 2, 2)
	require.NoError(t, err)
	assert.Len(t, trips, 2)

	// Page 3: limit=2, offset=4 → only 1 remaining.
	trips, err = store.ListTrips(ctx, nil, nil, nil, 2, 4)
	require.NoError(t, err)
	assert.Len(t, trips, 1)
}

func TestListTrips_EmptyResult(t *testing.T) {
	store := newTestStore(t)
	setupTripListTestData(t, store)
	ctx := context.Background()

	trips, err := store.ListTrips(ctx, nil, nil, nil, 50, 0)
	require.NoError(t, err)
	require.NotNil(t, trips, "empty result must be non-nil slice")
	assert.Len(t, trips, 0)
}

func TestGetTrip_Success(t *testing.T) {
	store := newTestStore(t)
	userID1, _ := setupTripListTestData(t, store)
	ctx := context.Background()

	tripID := insertTrip(t, store, userID1, "bus-list-1", "route-5", "completed")

	trip, err := store.GetTrip(ctx, tripID)
	require.NoError(t, err)
	require.NotNil(t, trip)

	assert.Equal(t, tripID, trip.ID)
	assert.Equal(t, userID1, trip.UserID)
	assert.Equal(t, "bus-list-1", trip.VehicleID)
	assert.Equal(t, "route-5", trip.RouteID)
	assert.Equal(t, "completed", trip.Status)
	assert.NotZero(t, trip.CreatedAt)
	assert.NotZero(t, trip.UpdatedAt)
}

func TestGetTrip_NotFound(t *testing.T) {
	store := newTestStore(t)
	setupTripListTestData(t, store)
	ctx := context.Background()

	_, err := store.GetTrip(ctx, 99999)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTripNotFound)
}

func TestGetTrip_NullableEndTime(t *testing.T) {
	store := newTestStore(t)
	userID1, _ := setupTripListTestData(t, store)
	ctx := context.Background()

	// Active trip — end_time should be nil.
	activeTripID := insertTrip(t, store, userID1, "bus-list-1", "route-1", "active")
	trip, err := store.GetTrip(ctx, activeTripID)
	require.NoError(t, err)
	assert.Nil(t, trip.EndTime, "active trip should have nil end_time")

	// End the trip so we can check completed.
	_, err = store.pool.Exec(ctx, "UPDATE trips SET status = 'completed', end_time = NOW() WHERE id = $1", activeTripID)
	require.NoError(t, err)

	trip, err = store.GetTrip(ctx, activeTripID)
	require.NoError(t, err)
	require.NotNil(t, trip.EndTime, "completed trip should have non-nil end_time")
	assert.False(t, trip.EndTime.IsZero(), "end_time should be a valid timestamp")
}
