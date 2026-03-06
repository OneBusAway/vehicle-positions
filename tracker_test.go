package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracker_Update(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)

	loc := &LocationReport{
		VehicleID: "bus-1",
		TripID:    "route-5",
		Latitude:  -1.29,
		Longitude: 36.82,
		Bearing:   180,
		Speed:     8.5,
		Timestamp: 1000000,
	}
	tracker.Update(loc)

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Equal(t, "bus-1", active[0].VehicleID)
	assert.Equal(t, "route-5", active[0].TripID)
	assert.Equal(t, -1.29, active[0].Latitude)
	assert.Equal(t, 36.82, active[0].Longitude)
	assert.Equal(t, 180.0, active[0].Bearing)
	assert.Equal(t, 8.5, active[0].Speed)
	assert.Equal(t, int64(1000000), active[0].Timestamp)
}

func TestTracker_UpdateOverwrites(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1.0, Longitude: 2.0, Timestamp: 1})
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 3.0, Longitude: 4.0, Timestamp: 2})

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Equal(t, 3.0, active[0].Latitude)
	assert.Equal(t, 4.0, active[0].Longitude)
}

func TestTracker_MultipleVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 1})
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: 2})

	active := tracker.ActiveVehicles()
	assert.Len(t, active, 2)
}

func TestTracker_Staleness(t *testing.T) {
	tracker := NewTracker(1 * time.Millisecond)

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 1})
	time.Sleep(5 * time.Millisecond)

	active := tracker.ActiveVehicles()
	assert.Len(t, active, 0, "stale vehicle should not appear in active list")
}

func TestTracker_Concurrent(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			tracker.Update(&LocationReport{
				VehicleID: "bus-1",
				Latitude:  float64(id),
				Longitude: float64(id),
				Timestamp: int64(id),
			})
		}(i)
		go func() {
			defer wg.Done()
			tracker.ActiveVehicles()
		}()
	}

	wg.Wait()
	active := tracker.ActiveVehicles()
	assert.Len(t, active, 1)
}
