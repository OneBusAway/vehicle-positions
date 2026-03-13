package main

import (
	"sync"
	"time"
)

// VehicleState holds the latest known position of a vehicle.
type VehicleState struct {
	VehicleID string
	TripID    string
	Latitude  float64
	Longitude float64
	Bearing   *float64
	Speed     *float64
	Timestamp int64
	UpdatedAt time.Time // server time when this report was received
}

// Tracker maintains an in-memory map of current vehicle positions.
type Tracker struct {
	mu       sync.RWMutex
	vehicles map[string]*VehicleState
	seen     map[string]struct{}
	maxAge   time.Duration
	done     chan struct{}
	stopOnce sync.Once
}

// NewTracker creates a Tracker with the given staleness threshold.
func NewTracker(maxAge time.Duration) *Tracker {
	if maxAge <= 0 {
		panic("maxAge must be positive")
	}

	t := &Tracker{
		vehicles: make(map[string]*VehicleState),
		seen:     make(map[string]struct{}),
		maxAge:   maxAge,
		done:     make(chan struct{}),
	}

	go func() {
		ticker := time.NewTicker(maxAge)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.cleanup()
			case <-t.done:
				return
			}
		}
	}()

	return t
}

func (t *Tracker) Stop() {
	t.stopOnce.Do(func() {
		close(t.done)
	})
}

// Update stores or replaces the latest position for a vehicle.
func (t *Tracker) Update(loc *LocationReport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen[loc.VehicleID] = struct{}{}

	var bearing *float64
	if loc.Bearing != nil {
		v := *loc.Bearing
		bearing = &v
	}

	var speed *float64
	if loc.Speed != nil {
		v := *loc.Speed
		speed = &v
	}

	t.vehicles[loc.VehicleID] = &VehicleState{
		VehicleID: loc.VehicleID,
		TripID:    loc.TripID,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Bearing:   bearing,
		Speed:     speed,
		Timestamp: loc.Timestamp,
		UpdatedAt: time.Now(),
	}
}

// ActiveVehicles returns vehicles that have reported within the staleness threshold.
func (t *Tracker) ActiveVehicles() []*VehicleState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cutoff := time.Now().Add(-t.maxAge)
	var active []*VehicleState
	for _, v := range t.vehicles {
		if v.UpdatedAt.After(cutoff) {
			copy := *v
			active = append(active, &copy)
		}
	}
	return active
}

// TrackerStatus holds aggregate statistics about tracked vehicles.
type TrackerStatus struct {
	ActiveVehicles       int
	TotalVehiclesTracked int
	LastUpdate           *time.Time
}

// Status returns aggregate statistics with a single lock acquisition.
func (t *Tracker) Status() TrackerStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cutoff := time.Now().Add(-t.maxAge)
	var s TrackerStatus
	s.TotalVehiclesTracked = len(t.seen)
	var latest time.Time
	for _, v := range t.vehicles {
		if v.UpdatedAt.After(cutoff) {
			s.ActiveVehicles++
		}
		if v.UpdatedAt.After(latest) {
			latest = v.UpdatedAt
		}
	}
	if !latest.IsZero() {
		s.LastUpdate = &latest
	}
	return s
}

// cleanup removes old entries from the tracker to prevent unbounded memory growth.
func (t *Tracker) cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-t.maxAge)
	for id, v := range t.vehicles {
		if v.UpdatedAt.Before(cutoff) {
			delete(t.vehicles, id)
		}
	}
}
