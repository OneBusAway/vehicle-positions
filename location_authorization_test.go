package main

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStore_AuthorizeLocation_SetsTripStart: a headway run's start_time in the
// feed is when its trip started, read from the trips row with every report,
// so it stays the same for the whole run and across a restart.
func TestStore_AuthorizeLocation_SetsTripStart(t *testing.T) {
	store := newTestStore(t)
	userID := setupTripTestData(t, store)
	ctx := context.Background()
	trip, err := store.StartTrip(ctx, userID, "bus-trip-1", "R1", "T1")
	require.NoError(t, err)

	loc := &LocationReport{DriverID: strconv.FormatInt(userID, 10), VehicleID: "bus-trip-1", TripID: "T1"}
	require.NoError(t, store.AuthorizeLocation(ctx, loc))
	require.False(t, trip.StartTime.IsZero())
	assert.True(t, loc.TripStartedAt.Equal(trip.StartTime), "got %v, the trip started at %v", loc.TripStartedAt, trip.StartTime)
}
