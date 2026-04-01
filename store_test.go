package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, skipping database test")
	}
	return url
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	url := testDatabaseURL(t)

	// Use require to fail fast if store creation or migration fails
	store, err := NewStore(context.Background(), url)
	require.NoError(t, err)

	err = store.Migrate(url)
	require.NoError(t, err)

	// Ensure the connection is closed when the test finishes
	t.Cleanup(func() {
		store.Close()
	})

	return store
}

func TestStore_NewStore(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}

	store, err := NewStore(context.Background(), url)
	assert.NoError(t, err)
	assert.NotNil(t, store)

	err = store.Migrate(url)
	assert.NoError(t, err, "Migrate should not fail")

	// This proves the schema exists without needing access to store.db
	err = store.SaveLocation(context.Background(), &LocationReport{
		VehicleID: "test-bus",
		Latitude:  12.34,
		Longitude: 56.78,
		Timestamp: 123456789,
	})
	assert.NoError(t, err, "Should be able to save a location after migration")
}

func TestStore_SaveLocation(t *testing.T) {
	store := newTestStore(t)
	url := testDatabaseURL(t)
	ctx := context.Background()

	store, err := NewStore(ctx, url)
	require.NoError(t, err)
	defer store.Close()

	// Clean up from prior runs
	_, err = store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{
		VehicleID: "test-bus-1",
		TripID:    "route-5",
		Latitude:  -1.29,
		Longitude: 36.82,
		Bearing:   float64ptr(180.0),
		Speed:     float64ptr(8.5),
		Accuracy:  float64ptr(12.0),
		Timestamp: 1752566400,
	}

	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	// Verify vehicle was created
	var vehicleID string
	err = store.pool.QueryRow(ctx, "SELECT id FROM vehicles WHERE id = $1", "test-bus-1").Scan(&vehicleID)
	require.NoError(t, err)
	assert.Equal(t, "test-bus-1", vehicleID)

	// Verify location was inserted
	var lat, lon float64
	var tripID string
	err = store.pool.QueryRow(ctx,
		"SELECT latitude, longitude, trip_id FROM location_points WHERE vehicle_id = $1",
		"test-bus-1",
	).Scan(&lat, &lon, &tripID)
	require.NoError(t, err)
	assert.Equal(t, -1.29, lat)
	assert.Equal(t, 36.82, lon)
	assert.Equal(t, "route-5", tripID)
}

func TestStore_SaveLocation_UpsertVehicle(t *testing.T) {
	store := newTestStore(t)
	url := testDatabaseURL(t)
	ctx := context.Background()

	store, err := NewStore(ctx, url)
	require.NoError(t, err)
	defer store.Close()

	// Clean up
	_, err = store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{VehicleID: "test-bus-2", Latitude: 1, Longitude: 2, Timestamp: 100}

	// Save twice — second should update vehicle, not fail
	require.NoError(t, store.SaveLocation(ctx, loc))
	require.NoError(t, store.SaveLocation(ctx, loc))

	// Should still be one vehicle
	var count int
	err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM vehicles WHERE id = $1", "test-bus-2").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// But two location points
	err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM location_points WHERE vehicle_id = $1", "test-bus-2").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestStore_GetRecentLocations(t *testing.T) {
	store := newTestStore(t)
	url := testDatabaseURL(t)
	ctx := context.Background()

	store, err := NewStore(ctx, url)
	require.NoError(t, err)
	defer store.Close()

	// Clean up
	_, err = store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	now := time.Now()

	// Insert an old location that should be filtered out
	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('bus-stale')")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ('bus-stale', '', 1, 1, 1, $1)", now.Add(-10*time.Minute))
	require.NoError(t, err)

	// Insert a recent location that should be included
	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('bus-fresh')")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ('bus-fresh', 'route-1', 2, 2, 2, $1)", now)
	require.NoError(t, err)

	// Insert an even more recent location for the same vehicle to test DISTINCT ON
	_, err = store.pool.Exec(ctx, "INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ('bus-fresh', 'route-1', 3, 3, 3, $1)", now.Add(1*time.Minute))
	require.NoError(t, err)

	// Query with a 5-minute staleness threshold
	cutoff := now.Add(-5 * time.Minute)
	locs, err := store.GetRecentLocations(ctx, cutoff)
	require.NoError(t, err)

	require.Len(t, locs, 1, "should only return 1 active vehicle")
	assert.Equal(t, "bus-fresh", locs[0].VehicleID)
	assert.Equal(t, float64(3), locs[0].Latitude, "should return the most recent point for the vehicle")
}

func TestStore_SaveLocation_CheckConstraints(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		loc       LocationReport
		vehicleID string
	}{
		{"empty vehicle ID", LocationReport{VehicleID: "", Latitude: 1, Longitude: 1, Timestamp: 1}, ""},
		{"latitude too high", LocationReport{VehicleID: "v", Latitude: 91, Longitude: 1, Timestamp: 1}, "v"},
		{"latitude too low", LocationReport{VehicleID: "v", Latitude: -91, Longitude: 1, Timestamp: 1}, "v"},
		{"longitude too high", LocationReport{VehicleID: "v", Latitude: 1, Longitude: 181, Timestamp: 1}, "v"},
		{"longitude too low", LocationReport{VehicleID: "v", Latitude: 1, Longitude: -181, Timestamp: 1}, "v"},
		{"zero timestamp", LocationReport{VehicleID: "v", Latitude: 1, Longitude: 1, Timestamp: 0}, "v"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.SaveLocation(ctx, &tt.loc)
			assert.Error(t, err)

			// Verify rollback: no stale vehicle or location rows were left behind
			if tt.vehicleID != "" {
				var count int
				err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM vehicles WHERE id = $1", tt.vehicleID).Scan(&count)
				require.NoError(t, err)
				assert.Equal(t, 0, count, "transaction should have rolled back, no vehicle row expected")
			}
		})
	}
}

func TestStore_SaveLocation_NullableFieldRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{
		VehicleID: "bus-nullable",
		TripID:    "route-1",
		Latitude:  -1.29,
		Longitude: 36.82,
		Bearing:   float64ptr(180.5),
		Speed:     float64ptr(8.5),
		Accuracy:  float64ptr(12.0),
		Timestamp: 1752566400,
	}

	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	cutoff := time.Now().Add(-5 * time.Minute)
	locs, err := store.GetRecentLocations(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, locs, 1)

	// updated as pointer fields, so we can distinguish between null and zero values
	require.NotNil(t, locs[0].Bearing)
	assert.Equal(t, 180.5, *locs[0].Bearing, "bearing should round-trip through save and read")

	require.NotNil(t, locs[0].Speed)
	assert.Equal(t, 8.5, *locs[0].Speed, "speed should round-trip through save and read")

	require.NotNil(t, locs[0].Accuracy)
	assert.Equal(t, 12.0, *locs[0].Accuracy, "accuracy should round-trip through save and read")
}

func TestStore_Migrate_Idempotent(t *testing.T) {
	store := newTestStore(t)
	err := store.Migrate(testDatabaseURL(t))
	assert.NoError(t, err, "second Migrate call should succeed")
}

func TestStore_SaveLocation_DriverIDPersisted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{
		VehicleID: "bus-driver-test",
		Latitude:  1.0,
		Longitude: 2.0,
		Timestamp: time.Now().Unix(),
		DriverID:  "99",
	}

	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	var driverID string
	err = store.pool.QueryRow(ctx,
		"SELECT driver_id FROM location_points WHERE vehicle_id = $1",
		"bus-driver-test",
	).Scan(&driverID)
	require.NoError(t, err)
	assert.Equal(t, "99", driverID, "driver_id must be persisted to location_points")
}

func TestStore_SaveLocation_DriverIDDefaultsToEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{
		VehicleID: "bus-no-driver",
		Latitude:  1.0,
		Longitude: 2.0,
		Timestamp: time.Now().Unix(),
		// DriverID intentionally omitted
	}

	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	var driverID string
	err = store.pool.QueryRow(ctx,
		"SELECT driver_id FROM location_points WHERE vehicle_id = $1",
		"bus-no-driver",
	).Scan(&driverID)
	require.NoError(t, err)
	assert.Equal(t, "", driverID, "driver_id must default to empty string when not set")
}

func TestStore_GetRecentLocations_ReturnsDriverID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{
		VehicleID: "bus-driver-recent",
		Latitude:  1.0,
		Longitude: 2.0,
		Timestamp: time.Now().Unix(),
		DriverID:  "77",
	}
	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	cutoff := time.Now().Add(-5 * time.Minute)
	locs, err := store.GetRecentLocations(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, locs, 1)
	assert.Equal(t, "77", locs[0].DriverID, "driver_id must round-trip through save and GetRecentLocations")
}

func TestStore_SaveLocation_MissingOptionalFieldsStoredAsNull(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	loc := &LocationReport{
		VehicleID: "bus-null",
		Latitude:  1,
		Longitude: 2,
		Timestamp: 100,
	}

	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	// Verify that the nullable fields are actually null in the database, not just zero
	locs, err := store.GetRecentLocations(ctx, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	require.Len(t, locs, 1)

	assert.Nil(t, locs[0].Bearing)
	assert.Nil(t, locs[0].Speed)
	assert.Nil(t, locs[0].Accuracy)
}

func TestStore_SaveLocation_ExplicitZeroOptionalFieldsPreserved(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	zero := 0.0
	loc := &LocationReport{
		VehicleID: "bus-zero",
		Latitude:  1,
		Longitude: 2,
		Bearing:   &zero,
		Speed:     &zero,
		Accuracy:  &zero,
		Timestamp: 100,
	}

	err = store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	locs, err := store.GetRecentLocations(ctx, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	require.Len(t, locs, 1)

	// Verify that the explicit zero values are preserved and not treated as null
	require.NotNil(t, locs[0].Bearing)
	assert.Equal(t, 0.0, *locs[0].Bearing)

	require.NotNil(t, locs[0].Speed)
	assert.Equal(t, 0.0, *locs[0].Speed)

	require.NotNil(t, locs[0].Accuracy)
	assert.Equal(t, 0.0, *locs[0].Accuracy)
}
