package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracker_Update(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	loc := &LocationReport{
		VehicleID: "bus-1",
		TripID:    "route-5",
		Latitude:  -1.29,
		Longitude: 36.82,
		Bearing:   float64ptr(180),
		Speed:     float64ptr(8.5),
		Timestamp: 1000000,
	}
	tracker.Update(loc)

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Equal(t, "bus-1", active[0].VehicleID)
	assert.Equal(t, "route-5", active[0].TripID)
	assert.Equal(t, -1.29, active[0].Latitude)
	assert.Equal(t, 36.82, active[0].Longitude)
	require.NotNil(t, active[0].Bearing)
	assert.Equal(t, 180.0, *active[0].Bearing)

	require.NotNil(t, active[0].Speed)
	assert.Equal(t, 8.5, *active[0].Speed)
	assert.Equal(t, int64(1000000), active[0].Timestamp)
}

func TestTracker_UpdateOverwrites(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1.0, Longitude: 2.0, Timestamp: 1})
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 3.0, Longitude: 4.0, Timestamp: 2})

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Equal(t, 3.0, active[0].Latitude)
	assert.Equal(t, 4.0, active[0].Longitude)
}

func TestTracker_MultipleVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 1})
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: 2})

	active := tracker.ActiveVehicles()
	assert.Len(t, active, 2)
}

func TestTracker_Staleness(t *testing.T) {
	tracker := NewTracker(1 * time.Millisecond)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 1})
	time.Sleep(5 * time.Millisecond)

	active := tracker.ActiveVehicles()
	assert.Len(t, active, 0, "stale vehicle should not appear in active list")
}

func TestTracker_Concurrent(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()
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

func TestTracker_ConcurrentWithCleanup(t *testing.T) {
	tracker := NewTracker(5 * time.Millisecond)
	defer tracker.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.Update(&LocationReport{
				VehicleID: fmt.Sprintf("bus-%d", id),
				Timestamp: 1,
			})
			tracker.ActiveVehicles()
		}(i)
	}
	wg.Wait()
}

func TestNewTracker_PanicsOnInvalidMaxAge(t *testing.T) {
	assert.PanicsWithValue(t, "maxAge must be positive", func() {
		NewTracker(0)
	}, "NewTracker(0) should panic")

	assert.PanicsWithValue(t, "maxAge must be positive", func() {
		NewTracker(-1 * time.Second)
	}, "NewTracker with negative duration should panic")
}

func TestTracker_Cleanup(t *testing.T) {

	maxAge := 10 * time.Millisecond
	tracker := NewTracker(maxAge)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Timestamp: 1})

	assert.Eventually(t, func() bool {
		tracker.mu.RLock()
		defer tracker.mu.RUnlock()
		return len(tracker.vehicles) == 0
	}, 100*time.Millisecond, 5*time.Millisecond, "stale vehicle should be deleted from internal map")
}

func TestTracker_StopIsIdempotent(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	tracker.Stop()
	assert.NotPanics(t, func() { tracker.Stop() })
}

func TestTracker_Status_Empty(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	s := tracker.Status()

	assert.Equal(t, 0, s.ActiveVehicles)
	assert.Equal(t, 0, s.TotalVehiclesTracked)
	assert.Nil(t, s.LastUpdate)
}

func TestTracker_Status_WithActiveVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 100})
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: 200})

	s := tracker.Status()

	assert.Equal(t, 2, s.ActiveVehicles)
	assert.Equal(t, 2, s.TotalVehiclesTracked)
	require.NotNil(t, s.LastUpdate)
	assert.False(t, s.LastUpdate.IsZero())
}

func TestTracker_Status_WithStaleVehicles(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "old-bus", Latitude: 1, Longitude: 2, Timestamp: 100})
	tracker.mu.Lock()
	tracker.vehicles["old-bus"].UpdatedAt = time.Now().Add(-10 * time.Minute)
	tracker.mu.Unlock()

	tracker.Update(&LocationReport{VehicleID: "new-bus", Latitude: 3, Longitude: 4, Timestamp: 200})

	s := tracker.Status()

	assert.Equal(t, 1, s.ActiveVehicles)
	assert.Equal(t, 2, s.TotalVehiclesTracked)
	require.NotNil(t, s.LastUpdate)
}

func TestTracker_TotalSeen_SurvivesCleanup(t *testing.T) {
	tracker := NewTracker(10 * time.Millisecond)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 1})
	tracker.Update(&LocationReport{VehicleID: "bus-2", Latitude: 3, Longitude: 4, Timestamp: 2})

	// Wait for cleanup to remove stale entries
	assert.Eventually(t, func() bool {
		tracker.mu.RLock()
		defer tracker.mu.RUnlock()
		return len(tracker.vehicles) == 0
	}, 100*time.Millisecond, 5*time.Millisecond, "cleanup should remove all stale vehicles")

	s := tracker.Status()
	assert.Equal(t, 0, s.ActiveVehicles)
	assert.Equal(t, 2, s.TotalVehiclesTracked, "totalSeen must survive cleanup")
}

func TestTracker_TotalSeen_DuplicateVehicleNotCounted(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 1, Longitude: 2, Timestamp: 1})
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 3, Longitude: 4, Timestamp: 2})
	tracker.Update(&LocationReport{VehicleID: "bus-1", Latitude: 5, Longitude: 6, Timestamp: 3})

	s := tracker.Status()
	assert.Equal(t, 1, s.TotalVehiclesTracked, "repeated updates for same vehicle should count once")
}

func TestTracker_CleanupPreservesFreshVehicles(t *testing.T) {
	maxAge := 50 * time.Millisecond
	tracker := NewTracker(maxAge)
	defer tracker.Stop()

	tracker.Update(&LocationReport{VehicleID: "old-bus", Timestamp: 1})
	time.Sleep(60 * time.Millisecond)
	tracker.Update(&LocationReport{VehicleID: "new-bus", Timestamp: 2})

	assert.Eventually(t, func() bool {
		tracker.mu.RLock()
		defer tracker.mu.RUnlock()
		_, hasOld := tracker.vehicles["old-bus"]
		_, hasNew := tracker.vehicles["new-bus"]
		return !hasOld && hasNew
	}, 200*time.Millisecond, 5*time.Millisecond,
		"cleanup should remove old-bus but keep new-bus")
}

func TestTracker_Update_PreservesNilOptionalFields(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	loc := &LocationReport{
		VehicleID: "bus-1",
		Latitude:  1,
		Longitude: 2,
		Timestamp: 100,
	}

	tracker.Update(loc)

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)
	assert.Nil(t, active[0].Bearing)
	assert.Nil(t, active[0].Speed)
}

func TestTracker_Update_PreservesExplicitZeroOptionalFields(t *testing.T) {
	tracker := NewTracker(5 * time.Minute)
	defer tracker.Stop()

	zero := 0.0
	loc := &LocationReport{
		VehicleID: "bus-1",
		Latitude:  1,
		Longitude: 2,
		Bearing:   &zero,
		Speed:     &zero,
		Timestamp: 100,
	}

	tracker.Update(loc)

	active := tracker.ActiveVehicles()
	require.Len(t, active, 1)

	require.NotNil(t, active[0].Bearing)
	assert.Equal(t, 0.0, *active[0].Bearing)

	require.NotNil(t, active[0].Speed)
	assert.Equal(t, 0.0, *active[0].Speed)
}
