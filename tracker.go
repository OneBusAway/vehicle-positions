package main

import (
	"sync"
	"time"
)

// VehicleState holds the latest known position of a vehicle.
type VehicleState struct {
	VehicleID string
	// VehicleLabel is the vehicle's label as of this report, so an admin
	// rename reaches the feed with the vehicle's next accepted report.
	VehicleLabel string
	TripID       string
	RouteID      string
	StartDate    string
	Latitude     float64
	Longitude    float64
	Bearing      *float64
	Speed        *float64
	Accuracy     *float64
	Timestamp    int64
	UpdatedAt    time.Time // server time when this report was received
	// StopID and StopSequence are the stop of its trip that the vehicle's
	// latest report put it at (StoppedAt) or heading to. StopID is "" when that
	// report named no stop (tripLocator.locate says when), and for a vehicle
	// seeded at startup until it next reports.
	StopID       string
	StopSequence int
	StoppedAt    bool
	// alongShape is how far along its trip's shape the vehicle was last
	// placed, in metres: the projection hint for its next report on that trip.
	// It is 0, the start of the shape, until the vehicle has been placed on
	// its current trip.
	alongShape float64
}

// Tracker maintains an in-memory map of current vehicle positions.
type Tracker struct {
	mu       sync.RWMutex
	vehicles map[string]*VehicleState
	seen     map[string]struct{}
	// removedAt is when Remove last dropped each vehicle, so UpdateAuthorized
	// can tell a report that raced a removal from one that came after it.
	removedAt map[string]time.Time
	// locator places each accepted report on its GTFS trip; nil places none,
	// which leaves every vehicle's stop fields unset.
	locator  *tripLocator
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

// SetTripLocator makes UpdateAuthorized place each vehicle on its GTFS trip
// with l, which is where the feed's stop fields come from.
func (t *Tracker) SetTripLocator(l *tripLocator) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.locator = l
}

// Update stores or replaces the latest position for a vehicle. It names no
// stop: it is how the tracker is seeded at startup, and a seeded vehicle is
// placed on its trip by its next report.
func (t *Tracker) Update(loc *LocationReport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.update(loc, tripPlace{})
}

// UpdateAuthorized is Update for a report whose authorization began at
// authorizedAt, placing the vehicle on its trip when a locator is set. A
// Remove since then wins and the report is not applied: the vehicle was
// deactivated while the report was being authorized and saved, and publishing
// it would put the vehicle back in the feed until it went stale. A vehicle
// removed before authorizedAt and reactivated since is updated as usual. It
// reports whether the update was applied.
func (t *Tracker) UpdateAuthorized(loc *LocationReport, authorizedAt time.Time) bool {
	place := t.place(loc)
	t.mu.Lock()
	defer t.mu.Unlock()
	if removed, ok := t.removedAt[loc.VehicleID]; ok && !removed.Before(authorizedAt) {
		return false
	}
	t.update(loc, place)
	return true
}

// place is where loc puts its vehicle on its trip. Only the hint is read under
// the lock: the projection is a pass over every segment of the shape, and the
// feed reads the tracker on every request.
func (t *Tracker) place(loc *LocationReport) tripPlace {
	t.mu.RLock()
	locator := t.locator
	// The hint is the vehicle's last place on this same trip instance. A new
	// trip, or the same trip on another service date, starts from the
	// beginning of its shape.
	var hint float64
	if prev, ok := t.vehicles[loc.VehicleID]; ok && prev.TripID == loc.TripID && prev.StartDate == loc.StartDate {
		hint = prev.alongShape
	}
	t.mu.RUnlock()
	if locator == nil {
		return tripPlace{}
	}
	return locator.locate(loc, hint)
}

// update is Update with t.mu already held, recording place as the vehicle's
// place on its trip.
func (t *Tracker) update(loc *LocationReport, place tripPlace) {
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
		VehicleID:    loc.VehicleID,
		VehicleLabel: loc.VehicleLabel,
		TripID:       loc.TripID,
		RouteID:      loc.RouteID,
		StartDate:    loc.StartDate,
		Latitude:     loc.Latitude,
		Longitude:    loc.Longitude,
		Bearing:      bearing,
		Speed:        speed,
		Accuracy:     accuracy,
		Timestamp:    loc.Timestamp,
		UpdatedAt:    time.Now(),
		StopID:       place.stopID,
		StopSequence: place.stopSequence,
		StoppedAt:    place.stoppedAt,
		alongShape:   place.alongShape,
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
