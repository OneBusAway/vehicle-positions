package main

import (
	"context"
	"errors"
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
	mustEndTrip(t, store, ctx, trip1.ID, driver1)

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
	mustEndTrip(t, store, ctx, trip1.ID, driver1)

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
	mustEndTrip(t, store, ctx, trip.ID, userID)
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

	vehicleID, err := store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)
	assert.Equal(t, "bus-trip-1", vehicleID, "the handler removes this vehicle from the feed")

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

	_, err := store.EndTrip(ctx, 99999, 1)
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
	_, err = store.EndTrip(ctx, trip.ID, userID+999)
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
	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)

	// End again — should fail.
	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTripNotFound, "ending an already-completed trip should return not found")
}

// TestStore_EndTripByID_FreesLockedOutDriver is the lockout from #164: a
// driver whose phone lost its trip cannot start another until something ends
// the old one.
func TestStore_EndTripByID_FreesLockedOutDriver(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "gtfs-5")
	require.NoError(t, err)
	_, err = store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "gtfs-5")
	require.ErrorIs(t, err, ErrActiveTripExists, "precondition: the driver is locked out")

	ended, err := store.EndTripByID(ctx, trip.ID)
	require.NoError(t, err)
	assert.Equal(t, trip.ID, ended.ID)
	assert.Equal(t, "completed", ended.Status)
	require.NotNil(t, ended.EndTime)
	assert.WithinDuration(t, time.Now(), *ended.EndTime, time.Minute)
	assert.Equal(t, userID, ended.UserID)
	assert.Equal(t, "Trip Driver", ended.DriverName)
	assert.Equal(t, "bus-trip-1", ended.VehicleID)
	assert.Equal(t, "Bus 1", ended.VehicleLabel)

	var status string
	err = store.pool.QueryRow(ctx, "SELECT status FROM trips WHERE id = $1", trip.ID).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)

	_, err = store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "gtfs-5")
	assert.NoError(t, err, "the driver must be able to start again once the trip is ended")
}

// TestStore_EndTripByID_EndsAnyDriversTrip verifies the admin end has no
// driver check, which is what sets it apart from EndTrip, and that it ends
// only the trip it names.
func TestStore_EndTripByID_EndsAnyDriversTrip(t *testing.T) {
	store := newTestStore(t)
	driverA := setupTripTestData(t, store)
	driverB := setupTripUser(t, store, "Other Driver", "other@test.com", "bus-trip-2", "Bus 2")
	ctx := context.Background()

	tripA, err := store.StartTrip(ctx, driverA, "bus-trip-1", "route-5", "")
	require.NoError(t, err)
	tripB, err := store.StartTrip(ctx, driverB, "bus-trip-2", "route-6", "")
	require.NoError(t, err)

	ended, err := store.EndTripByID(ctx, tripA.ID)
	require.NoError(t, err)
	assert.Equal(t, driverA, ended.UserID)
	assert.Equal(t, "completed", ended.Status)

	summaryB, err := store.GetTripSummary(ctx, tripB.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", summaryB.Status, "ending one trip must leave other drivers' trips running")
}

func TestStore_EndTripByID_NotFound(t *testing.T) {
	store := newTestStore(t)
	_ = setupTripTestData(t, store)

	trip, err := store.EndTripByID(context.Background(), 99999999)
	assert.ErrorIs(t, err, ErrTripNotFound)
	assert.Nil(t, trip)
}

// TestStore_EndTripByID_AlreadyEnded verifies an ended trip is refused with
// its own error and keeps the end_time it already had.
func TestStore_EndTripByID_AlreadyEnded(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)
	// The driver ended it an hour ago.
	endedAt := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	_, err = store.pool.Exec(ctx, "UPDATE trips SET status = 'completed', end_time = $2 WHERE id = $1", trip.ID, endedAt)
	require.NoError(t, err)

	ended, err := store.EndTripByID(ctx, trip.ID)
	assert.ErrorIs(t, err, ErrTripNotActive)
	assert.Nil(t, ended)

	var endAfter time.Time
	err = store.pool.QueryRow(ctx, "SELECT end_time FROM trips WHERE id = $1", trip.ID).Scan(&endAfter)
	require.NoError(t, err)
	assert.True(t, endAfter.Equal(endedAt), "an ended trip's end_time must not be overwritten: got %v, want %v", endAfter, endedAt)
}

// TestStore_EndTripByID_Twice verifies a second end of the same trip, such as
// two admins clicking at once, is refused rather than ending it again.
func TestStore_EndTripByID_Twice(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)

	_, err = store.EndTripByID(ctx, trip.ID)
	require.NoError(t, err)
	_, err = store.EndTripByID(ctx, trip.ID)
	assert.ErrorIs(t, err, ErrTripNotActive)
}

func TestStore_StartTrip_AfterEndingPrevious(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()

	// Start and end a trip.
	trip1, err := store.StartTrip(ctx, userID, "bus-trip-1", "route-5", "")
	require.NoError(t, err)
	_, err = store.EndTrip(ctx, trip1.ID, userID)
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

	_, err = store.EndTrip(ctx, trip.ID, userID)
	require.NoError(t, err)

	var afterUpdated time.Time
	err = store.pool.QueryRow(ctx, "SELECT updated_at FROM trips WHERE id = $1", trip.ID).Scan(&afterUpdated)
	require.NoError(t, err)
	assert.True(t, afterUpdated.After(beforeUpdated), "updated_at should advance after ending trip")
}

// TestStore_AuthorizeLocation walks the reported attack: a driver may
// publish only for a vehicle they are assigned to, that is active, and that
// they have an active trip on whose trip_id and route_id the report matches.
func TestStore_AuthorizeLocation(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	driver2 := setupTripUser(t, store, "Driver Two", "driver2-auth@test.com", "bus-102", "Bus 102")
	_, err := store.pool.Exec(ctx, "INSERT INTO vehicles (id, label) VALUES ('bus-101', 'Bus 101')")
	require.NoError(t, err)
	driverID := strconv.FormatInt(driver2, 10)
	report := func(vehicleID, tripID, routeID string) *LocationReport {
		return &LocationReport{DriverID: driverID, VehicleID: vehicleID, TripID: tripID, RouteID: routeID}
	}

	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("bus-101", "", "")), ErrNotAssigned, "vehicle assigned to someone else")
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("ghost-999", "", "")), ErrNotAssigned, "unknown vehicle")
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, &LocationReport{DriverID: "driver-1", VehicleID: "bus-102"}), ErrNotAssigned, "non-numeric subject")
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("bus-102", "", "")), ErrNoActiveTrip, "assigned but no trip")

	trip, err := store.StartTrip(ctx, driver2, "bus-102", "route-1", "gtfs-1")
	require.NoError(t, err)
	assert.NoError(t, store.AuthorizeLocation(ctx, report("bus-102", "gtfs-1", "route-1")))
	assert.NoError(t, store.AuthorizeLocation(ctx, report("bus-102", "", "")), "trip fields are optional")
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("bus-102", "FAKE-TRIP", "route-1")), ErrTripMismatch)
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("bus-102", "gtfs-1", "NOPE")), ErrTripMismatch)

	require.NoError(t, store.DeactivateVehicle(ctx, "bus-102"))
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("bus-102", "", "")), ErrVehicleInactive)

	_, err = store.pool.Exec(ctx, "UPDATE vehicles SET active = true WHERE id = 'bus-102'")
	require.NoError(t, err)
	mustEndTrip(t, store, ctx, trip.ID, driver2)
	assert.ErrorIs(t, store.AuthorizeLocation(ctx, report("bus-102", "", "")), ErrNoActiveTrip, "trip ended")

	var ghosts int
	require.NoError(t, store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM vehicles WHERE id = 'ghost-999'").Scan(&ghosts))
	assert.Zero(t, ghosts, "authorizing must never create a vehicle")
}

// TestStore_AuthorizeLocation_SetsVehicleLabel: authorizing a report already
// reads the vehicle row, so it hands the feed the label without a query per
// feed request, and reads it fresh, so a rename shows with the next report.
func TestStore_AuthorizeLocation_SetsVehicleLabel(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	driver := setupTripUser(t, store, "Label Driver", "label-auth@test.com", "bus-label", "Bus 42")
	driverID := strconv.FormatInt(driver, 10)

	refused := &LocationReport{DriverID: driverID, VehicleID: "bus-label"}
	require.ErrorIs(t, store.AuthorizeLocation(ctx, refused), ErrNoActiveTrip)
	assert.Empty(t, refused.VehicleLabel, "a refused report is not given a label")

	_, err := store.StartTrip(ctx, driver, "bus-label", "route-1", "gtfs-1")
	require.NoError(t, err)
	loc := &LocationReport{DriverID: driverID, VehicleID: "bus-label"}
	require.NoError(t, store.AuthorizeLocation(ctx, loc))
	assert.Equal(t, "Bus 42", loc.VehicleLabel)

	_, err = store.UpsertVehicle(ctx, "bus-label", "Bus 42A", "")
	require.NoError(t, err)
	loc = &LocationReport{DriverID: driverID, VehicleID: "bus-label"}
	require.NoError(t, store.AuthorizeLocation(ctx, loc))
	assert.Equal(t, "Bus 42A", loc.VehicleLabel, "a rename shows on the next report")
}

// TestStore_GetRecentLocations_SkipsInactiveVehicles keeps a restart from
// putting a deactivated vehicle back in the feed.
func TestStore_GetRecentLocations_SkipsInactiveVehicles(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	for _, id := range []string{"bus-on", "bus-off"} {
		require.NoError(t, store.SaveLocation(ctx, &LocationReport{VehicleID: id, Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}))
	}
	require.NoError(t, store.DeactivateVehicle(ctx, "bus-off"))
	onActiveTrips(t, store)

	locs, err := store.GetRecentLocations(ctx, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, locs, 1)
	assert.Equal(t, "bus-on", locs[0].VehicleID)
}

// TestStore_GetRecentLocations_SkipsEndedTrips keeps a restart from putting a
// vehicle back in the feed on a trip that has ended: the tracker forgets the
// end when the process stops, so seeding must not republish its last report.
func TestStore_GetRecentLocations_SkipsEndedTrips(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	running := setupTripUser(t, store, "Running Driver", "running@test.com", "bus-running", "Bus Running")
	done := setupTripUser(t, store, "Done Driver", "done@test.com", "bus-done", "Bus Done")
	_, err := store.StartTrip(ctx, running, "bus-running", "R1", "T1")
	require.NoError(t, err)
	ended, err := store.StartTrip(ctx, done, "bus-done", "R1", "T2")
	require.NoError(t, err)
	for _, id := range []string{"bus-running", "bus-done"} {
		require.NoError(t, store.SaveLocation(ctx, &LocationReport{VehicleID: id, Latitude: 1, Longitude: 2, Timestamp: time.Now().Unix()}))
	}
	_, err = store.EndTrip(ctx, ended.ID, done)
	require.NoError(t, err)

	locs, err := store.GetRecentLocations(ctx, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, locs, 1, "the vehicle whose trip ended is not seeded")
	assert.Equal(t, "bus-running", locs[0].VehicleID)
}

// TestStore_StartTrip_OneActiveTripPerVehicle: a second driver cannot start a
// trip on a vehicle another driver is running, and nobody can start one on a
// deactivated vehicle.
func TestStore_StartTrip_OneActiveTripPerVehicle(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	driverA := setupTripUser(t, store, "Driver A", "driver-a-busy@test.com", "bus-201", "Bus 201")
	driverB := setupTripUser(t, store, "Driver B", "driver-b-busy@test.com", "bus-201", "Bus 201")

	tripA, err := store.StartTrip(ctx, driverA, "bus-201", "R1", "T1")
	require.NoError(t, err)
	_, err = store.StartTrip(ctx, driverB, "bus-201", "R1", "T2")
	assert.ErrorIs(t, err, ErrVehicleBusy)

	mustEndTrip(t, store, ctx, tripA.ID, driverA)
	_, err = store.StartTrip(ctx, driverB, "bus-201", "R1", "T2")
	assert.NoError(t, err, "the vehicle is free once the first trip ends")

	setupTripUser(t, store, "Driver C", "driver-c-busy@test.com", "bus-102", "Bus 102")
	var driverC int64
	require.NoError(t, store.pool.QueryRow(ctx, "SELECT id FROM users WHERE email = 'driver-c-busy@test.com'").Scan(&driverC))
	require.NoError(t, store.DeactivateVehicle(ctx, "bus-102"))
	_, err = store.StartTrip(ctx, driverC, "bus-102", "R1", "T1")
	assert.ErrorIs(t, err, ErrVehicleInactive)
}

// TestStore_StartTrip_ConcurrentDriversOneVehicle races two drivers onto one
// vehicle: the unique index must let exactly one through.
func TestStore_StartTrip_ConcurrentDriversOneVehicle(t *testing.T) {
	store := newTestStore(t)
	clearTripTestData(t, store)
	t.Cleanup(func() { clearTripTestData(t, store) })
	ctx := context.Background()

	drivers := []int64{
		setupTripUser(t, store, "Racer A", "racer-a@test.com", "bus-race", "Bus Race"),
		setupTripUser(t, store, "Racer B", "racer-b@test.com", "bus-race", "Bus Race"),
	}
	errs := make(chan error, len(drivers))
	for _, d := range drivers {
		go func() {
			_, err := store.StartTrip(ctx, d, "bus-race", "R1", "T1")
			errs <- err
		}()
	}
	var ok, busy int
	for range drivers {
		err := <-errs
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrVehicleBusy):
			busy++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, 1, busy)
}

// mustEndTrip ends a trip and fails the test if it cannot.
func mustEndTrip(t *testing.T, store *Store, ctx context.Context, tripID, userID int64) {
	t.Helper()
	_, err := store.EndTrip(ctx, tripID, userID)
	require.NoError(t, err)
}

// holdVehicle parks every StartTrip on vehicleID between its assignment check
// and its insert, the window an unassign can land in: the holder transaction
// takes the vehicle row FOR UPDATE, so the start's FOR SHARE read of the
// vehicle, the step after its assignment check, waits for it. It returns the
// holder's backend pid, for blockedBehind, and release, which rolls the holder
// back and lets everything run on.
func holdVehicle(t *testing.T, store *Store, vehicleID string) (pid int, release func()) {
	t.Helper()
	ctx := context.Background()

	holder, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	_, err = holder.Exec(ctx, "SELECT 1 FROM vehicles WHERE id = $1 FOR UPDATE", vehicleID)
	require.NoError(t, err)
	require.NoError(t, holder.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid))
	return pid, func() { require.NoError(t, holder.Rollback(ctx)) }
}

// blockedBehind counts the backends blocked depth hops away from pid: 1 is
// whatever waits on pid itself, 2 is whatever waits on one of those. It polls
// through the pool, not pid's own connection, because pg_stat_activity is
// snapshotted once per transaction and that connection's would never update.
func blockedBehind(store *Store, pid, depth int) int {
	query := "SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))"
	if depth == 2 {
		query = `SELECT count(DISTINCT waiter.pid)
			FROM pg_stat_activity waiter, unnest(pg_blocking_pids(waiter.pid)) AS holder(pid)
			WHERE $1 = ANY(pg_blocking_pids(holder.pid))`
	}
	var n int
	if err := store.pool.QueryRow(context.Background(), query, pid).Scan(&n); err != nil {
		return 0
	}
	return n
}

// unassignDuringStart removes userID's assignment to vehicleID while a
// StartTrip for that pair is parked between its assignment check and its
// insert, then lets both finish. queued reports whether the unassign was seen
// waiting behind the start rather than finishing inside its window.
func unassignDuringStart(t *testing.T, store *Store, userID int64, vehicleID string) (startErr, unassignErr error, queued bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	holder, release := holdVehicle(t, store, vehicleID)

	started := make(chan error, 1)
	go func() {
		_, err := store.StartTrip(ctx, userID, vehicleID, "R1", "T1")
		started <- err
	}()
	require.Eventually(t, func() bool { return blockedBehind(store, holder, 1) == 1 },
		5*time.Second, 10*time.Millisecond, "the start never got past its assignment check")

	unassigned := make(chan error, 1)
	go func() { unassigned <- store.DeleteAssignment(ctx, userID, vehicleID) }()
	require.Eventually(t, func() bool { return len(unassigned) == 1 || blockedBehind(store, holder, 2) == 1 },
		5*time.Second, 10*time.Millisecond, "the unassign neither finished nor queued behind the start")
	// A queued unassign cannot finish while the start is parked.
	queued = len(unassigned) == 0

	release()
	return <-started, <-unassigned, queued
}

func assignmentExists(t *testing.T, store *Store, userID int64, vehicleID string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, store.pool.QueryRow(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM user_vehicles WHERE user_id = $1 AND vehicle_id = $2)",
		userID, vehicleID).Scan(&exists))
	return exists
}

func activeTripExists(t *testing.T, store *Store, userID int64, vehicleID string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, store.pool.QueryRow(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM trips WHERE user_id = $1 AND vehicle_id = $2 AND status = 'active')",
		userID, vehicleID).Scan(&exists))
	return exists
}

// TestStore_StartTrip_UnassignDuringStart lands an unassign between a trip
// start's assignment check and its insert. Whichever wins, the driver must not
// be left on a running trip on a vehicle they are no longer assigned to: the
// location ingest refuses every report from it, and the trip holds the driver
// and the vehicle until someone ends it.
func TestStore_StartTrip_UnassignDuringStart(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)

	unassignDuringStart(t, store, userID, "bus-trip-1")

	assert.False(t,
		activeTripExists(t, store, userID, "bus-trip-1") && !assignmentExists(t, store, userID, "bus-trip-1"),
		"the driver is on an active trip on a vehicle they are no longer assigned to")
}

// TestStore_DeleteAssignment_WaitsForAStartingTrip pins down how the test
// above passes: the unassign waits for the start holding the assignment, then
// sees the trip it committed and keeps the assignment.
func TestStore_DeleteAssignment_WaitsForAStartingTrip(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)

	startErr, unassignErr, queued := unassignDuringStart(t, store, userID, "bus-trip-1")

	assert.True(t, queued, "the unassign finished inside the start's window instead of waiting for it")
	require.NoError(t, startErr)
	assert.ErrorIs(t, unassignErr, ErrAssignmentInUse)
	assert.True(t, assignmentExists(t, store, userID, "bus-trip-1"), "a refused unassign must keep the assignment")
	assert.True(t, activeTripExists(t, store, userID, "bus-trip-1"))
}

// TestStore_StartTrip_WaitsForAnUncommittedUnassign is the other order: the
// unassign has deleted the assignment but not committed when the start checks
// it. The start waits for the unassign, then finds the assignment gone.
func TestStore_StartTrip_WaitsForAnUncommittedUnassign(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// unassign is DeleteAssignment stopped between its delete and its commit.
	unassign, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unassign.Rollback(context.Background()) })
	_, err = unassign.Exec(ctx, "DELETE FROM user_vehicles WHERE user_id = $1 AND vehicle_id = 'bus-trip-1'", userID)
	require.NoError(t, err)
	var unassignPID int
	require.NoError(t, unassign.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&unassignPID))

	started := make(chan error, 1)
	go func() {
		_, err := store.StartTrip(ctx, userID, "bus-trip-1", "R1", "T1")
		started <- err
	}()
	require.Eventually(t, func() bool { return len(started) == 1 || blockedBehind(store, unassignPID, 1) == 1 },
		5*time.Second, 10*time.Millisecond, "the start neither finished nor waited for the unassign")
	require.NoError(t, unassign.Commit(ctx))

	assert.ErrorIs(t, <-started, ErrNotAssigned)
	assert.False(t, activeTripExists(t, store, userID, "bus-trip-1"))
}

// TestStore_StartTrip_TwoStartsAndAnUnassignDoNotDeadlock overlaps two starts
// by one driver, from two phones, with an unassign of that driver. Both starts
// share-lock the same assignment row and the unassign waits on the pair. When
// they run on, one trip starts, the other start is told the driver already
// has one, and the unassign is refused, with no deadlock among the three.
func TestStore_StartTrip_TwoStartsAndAnUnassignDoNotDeadlock(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	holder, release := holdVehicle(t, store, "bus-trip-1")

	started := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := store.StartTrip(ctx, userID, "bus-trip-1", "R1", "T1")
			started <- err
		}()
	}
	require.Eventually(t, func() bool { return blockedBehind(store, holder, 1) == 2 },
		5*time.Second, 10*time.Millisecond, "the starts never both got past their assignment checks")

	unassigned := make(chan error, 1)
	go func() { unassigned <- store.DeleteAssignment(ctx, userID, "bus-trip-1") }()
	require.Eventually(t, func() bool { return blockedBehind(store, holder, 2) == 1 },
		5*time.Second, 10*time.Millisecond, "the unassign never queued behind the starts")

	release()

	// A deadlock would come back here as an error with SQLSTATE 40P01.
	var ok, alreadyActive int
	for range 2 {
		switch err := <-started; {
		case err == nil:
			ok++
		case errors.Is(err, ErrActiveTripExists):
			alreadyActive++
		default:
			t.Errorf("unexpected start error: %v", err)
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, 1, alreadyActive)
	assert.ErrorIs(t, <-unassigned, ErrAssignmentInUse)
	assert.True(t, assignmentExists(t, store, userID, "bus-trip-1"))
}
