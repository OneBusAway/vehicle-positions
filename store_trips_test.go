package main

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearTripTestData empties trips and every table holding a foreign key to
// the rows setupTripTestData creates, in dependency order, so tests start
// from (and leave behind) a known-empty state regardless of what other
// tests in the suite do with vehicles/users.
func clearTripTestData(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"location_points", "trips", "user_vehicles", "vehicles", "users"} {
		_, err := store.pool.Exec(ctx, "DELETE FROM "+table)
		require.NoError(t, err)
	}
}

// setupTripTestData creates a user, a vehicle, and assigns them for trip tests.
// Returns the user ID.
func setupTripTestData(t *testing.T, store *Store) int64 {
	t.Helper()
	ctx := context.Background()

	// Clean up in correct order (respect FK constraints), both before (in
	// case a prior test panicked before its t.Cleanup ran) and after.
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })

	// Create a test user.
	var userID int64
	err := store.pool.QueryRow(ctx,
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

// setupTripUser creates a driver user, a vehicle (if it doesn't already
// exist), and an assignment between them, returning the user ID. Unlike
// setupTripTestData it does not clear existing rows first — callers that
// need multiple drivers/vehicles in one test call this once per driver
// after clearing state themselves.
func setupTripUser(t *testing.T, store *Store, name, email, vehicleID, vehicleLabel string) int64 {
	t.Helper()
	ctx := context.Background()

	var userID int64
	err := store.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ($1, $2, '$2a$10$dummyhash000000000000000000000000000000000000000000', 'driver')
		 RETURNING id`,
		name, email,
	).Scan(&userID)
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx,
		"INSERT INTO vehicles (id, label) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING",
		vehicleID, vehicleLabel)
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO user_vehicles (user_id, vehicle_id) VALUES ($1, $2)", userID, vehicleID)
	require.NoError(t, err)

	return userID
}

func TestListTripsFiltersAndOrder(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	driver1 := setupTripUser(t, store, "Alice Driver", "alice-trips@test.com", "bus-list-1", "Bus List 1")
	trip1, err := store.StartTrip(ctx, driver1, "bus-list-1", "route-1", "gtfs-1")
	require.NoError(t, err)
	require.NoError(t, store.EndTrip(ctx, trip1.ID, driver1))

	// Small delay to ensure the second trip's start_time is strictly newer,
	// so the "newest first" ordering assertion below is deterministic.
	time.Sleep(time.Millisecond)

	driver2 := setupTripUser(t, store, "Bob Driver", "bob-trips@test.com", "bus-list-2", "Bus List 2")
	_, err = store.StartTrip(ctx, driver2, "bus-list-2", "route-2", "gtfs-2")
	require.NoError(t, err)

	all, err := store.ListTrips(ctx, TripFilter{Limit: 200})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 2)
	// newest first
	for i := 1; i < len(all); i++ {
		assert.True(t, !all[i-1].StartTime.Before(all[i].StartTime))
	}

	active, err := store.ListTrips(ctx, TripFilter{Status: "active", Limit: 200})
	require.NoError(t, err)
	require.NotEmpty(t, active)
	for _, tr := range active {
		assert.Equal(t, "active", tr.Status)
	}

	byVehicle, err := store.ListTrips(ctx, TripFilter{VehicleID: "bus-list-1", Limit: 200})
	require.NoError(t, err)
	require.NotEmpty(t, byVehicle)
	for _, tr := range byVehicle {
		assert.Equal(t, "bus-list-1", tr.VehicleID)
	}

	byQ, err := store.ListTrips(ctx, TripFilter{Q: "Alice", Limit: 200})
	require.NoError(t, err)
	assert.NotEmpty(t, byQ)

	page1, err := store.ListTrips(ctx, TripFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, page1, 1)
	page2, err := store.ListTrips(ctx, TripFilter{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.NotEqual(t, page1[0].ID, page2[0].ID)
}

// TestListTripsUserIDFilter covers the user_id filter: it must narrow results
// to one driver, compose with the other filters, and treat 0 as "all drivers"
// so the zero value cannot silently hide rows.
func TestListTripsUserIDFilter(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	driver1 := setupTripUser(t, store, "Cara Driver", "cara-trips@test.com", "bus-user-1", "Bus User 1")
	trip1, err := store.StartTrip(ctx, driver1, "bus-user-1", "route-1", "gtfs-1")
	require.NoError(t, err)
	require.NoError(t, store.EndTrip(ctx, trip1.ID, driver1))

	driver2 := setupTripUser(t, store, "Dan Driver", "dan-trips@test.com", "bus-user-2", "Bus User 2")
	_, err = store.StartTrip(ctx, driver2, "bus-user-2", "route-2", "gtfs-2")
	require.NoError(t, err)

	byUser, err := store.ListTrips(ctx, TripFilter{UserID: driver1, Limit: 200})
	require.NoError(t, err)
	require.NotEmpty(t, byUser)
	for _, tr := range byUser {
		assert.Equal(t, driver1, tr.UserID)
	}

	// Composes with status: driver1's only trip is completed, so filtering
	// for their active trips must come back empty rather than falling back
	// to an unfiltered list.
	activeForUser1, err := store.ListTrips(ctx, TripFilter{UserID: driver1, Status: "active", Limit: 200})
	require.NoError(t, err)
	assert.Empty(t, activeForUser1)

	activeForUser2, err := store.ListTrips(ctx, TripFilter{UserID: driver2, Status: "active", Limit: 200})
	require.NoError(t, err)
	require.Len(t, activeForUser2, 1)
	assert.Equal(t, driver2, activeForUser2[0].UserID)

	// A user with no trips at all yields an empty result, not everyone's.
	absent, err := store.ListTrips(ctx, TripFilter{UserID: driver2 + 100_000, Limit: 200})
	require.NoError(t, err)
	assert.Empty(t, absent)

	// Zero means "no driver filter": both drivers' trips come back.
	all, err := store.ListTrips(ctx, TripFilter{UserID: 0, Limit: 200})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(all), 2)
}

func TestGetTripSummary(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-9", "gtfs-9")
	require.NoError(t, err)

	summary, err := store.GetTripSummary(ctx, trip.ID)
	require.NoError(t, err)
	assert.Equal(t, trip.ID, summary.ID)
	assert.Equal(t, "bus-trip-1", summary.VehicleID)
	assert.Equal(t, "Bus 1", summary.VehicleLabel)
	assert.Equal(t, userID, summary.UserID)
	assert.Equal(t, "Trip Driver", summary.DriverName)
	assert.Equal(t, "route-9", summary.RouteID)
	assert.Equal(t, "gtfs-9", summary.GtfsTripID)
	assert.Equal(t, "active", summary.Status)
	assert.Nil(t, summary.EndTime)

	_, err = store.GetTripSummary(ctx, 99999999)
	assert.ErrorIs(t, err, ErrTripNotFound)
}

func TestListTripLocationsWindow(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "gtfs-5")
	require.NoError(t, err)

	driverIDStr := strconv.FormatInt(userID, 10)
	otherDriverIDStr := strconv.FormatInt(userID+999999, 10)

	time.Sleep(time.Millisecond)

	// In window: correct driver + vehicle, between start_time and end_time.
	require.NoError(t, store.SaveLocation(ctx, &LocationReport{
		VehicleID: "bus-trip-1",
		DriverID:  driverIDStr,
		Latitude:  1.0,
		Longitude: 2.0,
		Timestamp: time.Now().Unix(),
	}))

	// Excluded: wrong driver_id, same vehicle and time window.
	require.NoError(t, store.SaveLocation(ctx, &LocationReport{
		VehicleID: "bus-trip-1",
		DriverID:  otherDriverIDStr,
		Latitude:  1.1,
		Longitude: 2.1,
		Timestamp: time.Now().Unix(),
	}))

	time.Sleep(time.Millisecond)
	require.NoError(t, store.EndTrip(ctx, trip.ID, userID))
	time.Sleep(time.Millisecond)

	// Excluded: correct driver+vehicle but received after end_time.
	require.NoError(t, store.SaveLocation(ctx, &LocationReport{
		VehicleID: "bus-trip-1",
		DriverID:  driverIDStr,
		Latitude:  1.2,
		Longitude: 2.2,
		Timestamp: time.Now().Unix(),
	}))

	pts, err := store.ListTripLocations(ctx, trip.ID)
	require.NoError(t, err)
	require.Len(t, pts, 1)
	assert.InDelta(t, 1.0, pts[0].Latitude, 0.0001)
	assert.InDelta(t, 2.0, pts[0].Longitude, 0.0001)
}

func TestListActiveTripsByVehicleTiebreak(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	sharedVehicleID := "bus-tiebreak-1"

	// Two different drivers assigned to the SAME vehicle. The unique active-trip
	// index is per user, so both are allowed to have an active trip at once.
	driver1 := setupTripUser(t, store, "Driver One", "driver1-tiebreak@test.com", sharedVehicleID, "Bus Tiebreak")
	driver2 := setupTripUser(t, store, "Driver Two", "driver2-tiebreak@test.com", sharedVehicleID, "Bus Tiebreak")

	firstTrip, err := store.StartTrip(ctx, driver1, sharedVehicleID, "route-1", "gtfs-1")
	require.NoError(t, err)

	// Force the first trip's start_time strictly earlier so the second trip
	// is unambiguously newer, regardless of DB clock resolution.
	_, err = store.pool.Exec(ctx, "UPDATE trips SET start_time = $1 WHERE id = $2",
		time.Now().Add(-1*time.Minute), firstTrip.ID)
	require.NoError(t, err)

	secondTrip, err := store.StartTrip(ctx, driver2, sharedVehicleID, "route-2", "gtfs-2")
	require.NoError(t, err)

	m, err := store.ListActiveTripsByVehicle(ctx)
	require.NoError(t, err)
	info, ok := m[sharedVehicleID]
	require.True(t, ok)
	assert.Equal(t, secondTrip.ID, info.TripID, "newest active trip wins")
	assert.Equal(t, driver2, info.UserID)
	assert.Equal(t, "Driver Two", info.DriverName)
	assert.Equal(t, "route-2", info.RouteID)
	assert.Equal(t, "gtfs-2", info.GtfsTripID)
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

	// The rest should fail with ErrActiveTripExists (enforced by unique partial index).
	var failCount int
	for err := range failures {
		failCount++
		assert.ErrorIs(t, err, ErrActiveTripExists,
			"concurrent StartTrip should fail with ErrActiveTripExists, got: %v", err)
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

	var beforeUpdated time.Time
	err = store.pool.QueryRow(ctx, "SELECT updated_at FROM trips WHERE id = $1", trip.ID).Scan(&beforeUpdated)
	require.NoError(t, err)

	// Small delay to ensure the DB clock advances.
	time.Sleep(time.Millisecond)

	err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)

	var afterUpdated time.Time
	err = store.pool.QueryRow(ctx, "SELECT updated_at FROM trips WHERE id = $1", trip.ID).Scan(&afterUpdated)
	require.NoError(t, err)
	assert.True(t, afterUpdated.After(beforeUpdated), "updated_at should advance after ending trip")
}
