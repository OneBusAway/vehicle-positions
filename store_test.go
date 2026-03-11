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
	store.pool.Exec(ctx, "DELETE FROM location_points")
	store.pool.Exec(ctx, "DELETE FROM vehicles")

	loc := &LocationReport{
		VehicleID: "test-bus-1",
		TripID:    "route-5",
		Latitude:  -1.29,
		Longitude: 36.82,
		Bearing:   180.0,
		Speed:     8.5,
		Accuracy:  12.0,
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
	store.pool.Exec(ctx, "DELETE FROM location_points")
	store.pool.Exec(ctx, "DELETE FROM vehicles")

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
	store.pool.Exec(ctx, "DELETE FROM location_points")
	store.pool.Exec(ctx, "DELETE FROM vehicles")

	now := time.Now()

	// Insert an old location that should be filtered out
	store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('bus-stale')")
	store.pool.Exec(ctx, "INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ('bus-stale', '', 1, 1, 1, $1)", now.Add(-10*time.Minute))

	// Insert a recent location that should be included
	store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('bus-fresh')")
	store.pool.Exec(ctx, "INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ('bus-fresh', 'route-1', 2, 2, 2, $1)", now)

	// Insert an even more recent location for the same vehicle to test DISTINCT ON
	store.pool.Exec(ctx, "INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp, received_at) VALUES ('bus-fresh', 'route-1', 3, 3, 3, $1)", now.Add(1*time.Minute))

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

	store.pool.Exec(ctx, "DELETE FROM location_points")
	store.pool.Exec(ctx, "DELETE FROM vehicles")

	loc := &LocationReport{
		VehicleID: "bus-nullable",
		TripID:    "route-1",
		Latitude:  -1.29,
		Longitude: 36.82,
		Bearing:   180.5,
		Speed:     8.5,
		Accuracy:  12.0,
		Timestamp: 1752566400,
	}

	err := store.SaveLocation(ctx, loc)
	require.NoError(t, err)

	cutoff := time.Now().Add(-5 * time.Minute)
	locs, err := store.GetRecentLocations(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, locs, 1)

	assert.Equal(t, 180.5, locs[0].Bearing, "bearing should round-trip through save and read")
	assert.Equal(t, 8.5, locs[0].Speed, "speed should round-trip through save and read")
	assert.Equal(t, 12.0, locs[0].Accuracy, "accuracy should round-trip through save and read")
}

func TestStore_Migrate_Idempotent(t *testing.T) {
	store := newTestStore(t)
	err := store.Migrate(testDatabaseURL(t))
	assert.NoError(t, err, "second Migrate call should succeed")
}
