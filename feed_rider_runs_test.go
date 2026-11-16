package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// TestBuildFeed_ConcurrentRiderRunsAreTwoVehicles: two runs of one headway
// trip on one service day share trip_id and start_date, and their start
// times tell them apart, in the entity and vehicle ids as in the trip
// descriptor. E052 requires vehicle.id to be unique across the feed. A trip
// run by timetable keeps the id it had.
func TestBuildFeed_ConcurrentRiderRunsAreTwoVehicles(t *testing.T) {
	early, late, timetabled := sampleEstimate(), sampleEstimate(), sampleEstimate()
	early.StartTime, early.FrequencyBased = "08:00:00", true
	late.StartTime, late.FrequencyBased = "08:05:00", true
	timetabled.Key.TripID = "T3"

	feed := buildFeed(nil, []rider.TripEstimate{early, late, timetabled})
	require.Len(t, feed.Entity, 3)
	var ids, vehicleIDs []string
	for _, e := range feed.Entity {
		ids = append(ids, e.GetId())
		vehicleIDs = append(vehicleIDs, e.GetVehicle().GetVehicle().GetId())
	}
	assert.Equal(t, []string{"rider:T1:20260902:08:00:00", "rider:T1:20260902:08:05:00", "rider:T3:20260902"}, ids)
	assert.Equal(t, ids, vehicleIDs)
}
