package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_GetLocationHistory_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('hist-bus-1')")
	require.NoError(t, err)

	now := time.Now().Unix()
	_, err = store.pool.Exec(ctx,
		"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		"hist-bus-1", "trip-1", -1.29, 36.82, 180.0, 8.5, 12.0, now)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx,
		"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		"hist-bus-1", "trip-1", -1.30, 36.83, 90.0, 10.0, 5.0, now-60)
	require.NoError(t, err)

	points, err := store.GetLocationHistory(ctx, "hist-bus-1", 0, now, 100)
	require.NoError(t, err)
	require.Len(t, points, 2)

	// Most recent first
	assert.Equal(t, now, points[0].Timestamp)
	assert.Equal(t, now-60, points[1].Timestamp)
	assert.Equal(t, -1.29, points[0].Latitude)
	assert.Equal(t, 36.82, points[0].Longitude)
	assert.Equal(t, "trip-1", points[0].TripID)

	require.NotNil(t, points[0].Bearing)
	assert.Equal(t, 180.0, *points[0].Bearing)
	require.NotNil(t, points[0].Speed)
	assert.Equal(t, 8.5, *points[0].Speed)
	require.NotNil(t, points[0].Accuracy)
	assert.Equal(t, 12.0, *points[0].Accuracy)
}

func TestStore_GetLocationHistory_TimeRange(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('hist-bus-range')")
	require.NoError(t, err)

	now := time.Now().Unix()
	// Insert 3 points at different timestamps
	for _, ts := range []int64{now - 300, now - 100, now} {
		_, err = store.pool.Exec(ctx,
			"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp) VALUES ($1, '', $2, $3, $4)",
			"hist-bus-range", 1.0, 2.0, ts)
		require.NoError(t, err)
	}

	// Query only the middle range
	points, err := store.GetLocationHistory(ctx, "hist-bus-range", now-200, now-50, 100)
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, now-100, points[0].Timestamp)
}

func TestStore_GetLocationHistory_Limit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('hist-bus-limit')")
	require.NoError(t, err)

	now := time.Now().Unix()
	for i := range 5 {
		_, err = store.pool.Exec(ctx,
			"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp) VALUES ($1, '', $2, $3, $4)",
			"hist-bus-limit", 1.0, 2.0, now-int64(i))
		require.NoError(t, err)
	}

	points, err := store.GetLocationHistory(ctx, "hist-bus-limit", 0, now, 2)
	require.NoError(t, err)
	assert.Len(t, points, 2, "should respect limit")
}

func TestStore_GetLocationHistory_EmptyResult(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('hist-bus-empty')")
	require.NoError(t, err)

	points, err := store.GetLocationHistory(ctx, "hist-bus-empty", 0, time.Now().Unix(), 100)
	require.NoError(t, err)
	require.NotNil(t, points, "should return empty slice, not nil")
	assert.Len(t, points, 0)
}

func TestStore_GetLocationHistory_NullableFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('hist-bus-null')")
	require.NoError(t, err)

	now := time.Now().Unix()
	// Insert with NULL bearing/speed/accuracy
	_, err = store.pool.Exec(ctx,
		"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, timestamp) VALUES ($1, '', $2, $3, $4)",
		"hist-bus-null", 1.0, 2.0, now)
	require.NoError(t, err)

	// Insert with zero bearing (should be distinct from nil)
	_, err = store.pool.Exec(ctx,
		"INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp) VALUES ($1, '', $2, $3, $4, $5, $6, $7)",
		"hist-bus-null", 3.0, 4.0, 0.0, 0.0, 0.0, now-60)
	require.NoError(t, err)

	points, err := store.GetLocationHistory(ctx, "hist-bus-null", 0, now, 100)
	require.NoError(t, err)
	require.Len(t, points, 2)

	// Most recent: NULL fields
	assert.Nil(t, points[0].Bearing, "NULL bearing should be nil")
	assert.Nil(t, points[0].Speed, "NULL speed should be nil")
	assert.Nil(t, points[0].Accuracy, "NULL accuracy should be nil")

	// Older: zero-valued fields (not nil)
	require.NotNil(t, points[1].Bearing, "zero bearing should not be nil")
	assert.Equal(t, 0.0, *points[1].Bearing)
	require.NotNil(t, points[1].Speed, "zero speed should not be nil")
	assert.Equal(t, 0.0, *points[1].Speed)
	require.NotNil(t, points[1].Accuracy, "zero accuracy should not be nil")
	assert.Equal(t, 0.0, *points[1].Accuracy)
}

func TestStore_VehicleExists_Found(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, "INSERT INTO vehicles (id) VALUES ('exists-bus')")
	require.NoError(t, err)

	exists, err := store.VehicleExists(ctx, "exists-bus")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestStore_VehicleExists_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	exists, err := store.VehicleExists(ctx, "nonexistent-bus-999")
	require.NoError(t, err)
	assert.False(t, exists)
}
