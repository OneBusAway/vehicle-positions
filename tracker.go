package main

import (
	"sync"
	"time"
)

// VehicleState holds the latest known position of a vehicle.
type VehicleState struct {
	VehicleID string
	TripID    string
	RouteID   string
	StartDate string
	Latitude  float64
	Longitude float64
	Bearing   *float64
	Speed     *float64
	Accuracy  *float64
	Timestamp int64
	UpdatedAt time.Time // server time when this report was received
}

// Tracker maintains an in-memory map of current vehicle positions.
type Tracker struct {
	mu       sync.RWMutex
	vehicles map[string]*VehicleState
	seen     map[string]struct{}
	// removedAt is when Remove last dropped each vehicle, so UpdateAuthorized
	// can tell a report that raced a removal from one that came after it.
	removedAt map[string]time.Time
	maxAge    time.Duration
	done      chan struct{}
	stopOnce  sync.Once
}

// NewTracker creates a Tracker with the given staleness threshold.
func NewTracker(maxAge time.Duration) *Tracker {
	if maxAge <= 0 {
		panic("maxAge must be positive")
	}

	t := &Tracker{
		vehicles:  make(map[string]*VehicleState),
		seen:      make(map[string]struct{}),
		removedAt: make(map[string]time.Time),
		maxAge:    maxAge,
		done:      make(chan struct{}),
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
	t.update(loc)
}

// UpdateAuthorized is Update for a report whose authorization began at
// authorizedAt. A Remove since then wins and the report is not applied: the
// vehicle was deactivated while the report was being authorized and saved, and
// publishing it would put the vehicle back in the feed until it went stale. A
// vehicle removed before authorizedAt and reactivated since is updated as
// usual. It reports whether the update was applied.
func (t *Tracker) UpdateAuthorized(loc *LocationReport, authorizedAt time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if removed, ok := t.removedAt[loc.VehicleID]; ok && !removed.Before(authorizedAt) {
		return false
	}
	t.update(loc)
	return true
}

// update is Update with t.mu already held.
func (t *Tracker) update(loc *LocationReport) {
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

	var accuracy *float64
	if loc.Accuracy != nil {
		v := *loc.Accuracy
		accuracy = &v
	}

	t.vehicles[loc.VehicleID] = &VehicleState{
		VehicleID: loc.VehicleID,
		TripID:    loc.TripID,
		RouteID:   loc.RouteID,
		StartDate: loc.StartDate,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Bearing:   bearing,
		Speed:     speed,
		Accuracy:  accuracy,
		Timestamp: loc.Timestamp,
		UpdatedAt: time.Now(),
	}
}

// Remove drops a vehicle's latest position, so it leaves the feed now rather
// than when it goes stale. Used when an admin deactivates the vehicle.
func (t *Tracker) Remove(vehicleID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.vehicles, vehicleID)
	t.removedAt[vehicleID] = time.Now()
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
			if v.Bearing != nil {
				b := *v.Bearing
				copy.Bearing = &b
			}
			if v.Speed != nil {
				s := *v.Speed
				copy.Speed = &s
			}
			if v.Accuracy != nil {
				a := *v.Accuracy
				copy.Accuracy = &a
			}
			active = append(active, &copy)
		}
	}
	return active
}

// VehicleOnTrip returns the freshest vehicle reporting tripID within the
// staleness threshold, if any. It is how the rider engine learns that the
// agency's own driver half already speaks for a trip.
func (t *Tracker) VehicleOnTrip(tripID string) (VehicleState, bool) {
	if tripID == "" {
		return VehicleState{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	cutoff := time.Now().Add(-t.maxAge)
	var found *VehicleState
	for _, v := range t.vehicles {
		if v.TripID != tripID || !v.UpdatedAt.After(cutoff) {
			continue
		}
		if found == nil || v.Timestamp > found.Timestamp {
			found = v
		}
	}
	if found == nil {
		return VehicleState{}, false
	}
	// Update replaces the whole entry rather than mutating it, so the copy's
	// pointer fields stay valid.
	return *found, true
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
	// No report takes the staleness window to authorize and save, so an older
	// removal can no longer race one.
	for id, at := range t.removedAt {
		if at.Before(cutoff) {
			delete(t.removedAt, id)
		}
	}
}
