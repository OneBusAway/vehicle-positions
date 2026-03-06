package main

import (
	"context"
	"os"
	"testing"

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

func TestStore_NewStore(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()

	store, err := NewStore(ctx, url)
	require.NoError(t, err)
	defer store.Close()

	// Verify tables exist by querying them
	var count int
	err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM vehicles").Scan(&count)
	assert.NoError(t, err)

	err = store.pool.QueryRow(ctx, "SELECT COUNT(*) FROM location_points").Scan(&count)
	assert.NoError(t, err)
}

func TestStore_SaveLocation(t *testing.T) {
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
