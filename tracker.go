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
	Bearing   float64
	Speed     float64
	Timestamp int64
	UpdatedAt time.Time // server time when this report was received
}

// Tracker maintains an in-memory map of current vehicle positions.
type Tracker struct {
	mu       sync.RWMutex
	vehicles map[string]*VehicleState
	maxAge   time.Duration
}

// NewTracker creates a Tracker with the given staleness threshold.
func NewTracker(maxAge time.Duration) *Tracker {
	return &Tracker{
		vehicles: make(map[string]*VehicleState),
		maxAge:   maxAge,
	}
}

// Update stores or replaces the latest position for a vehicle.
func (t *Tracker) Update(loc *LocationReport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.vehicles[loc.VehicleID] = &VehicleState{
		VehicleID: loc.VehicleID,
		TripID:    loc.TripID,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Bearing:   loc.Bearing,
		Speed:     loc.Speed,
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
			active = append(active, v)
		}
	}
	return active
}
