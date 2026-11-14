package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// The default thresholds. A vehicle leaves the feed STALENESS_THRESHOLD (5m
// by default) after its last report, so a quiet vehicle is flagged before it
// drops off the map.
const (
	defaultAlertQuietAfter   = 3 * time.Minute
	defaultAlertStoppedAfter = 10 * time.Minute
)

// stoppedRadius is how far, in metres, a vehicle has to get from where it last
// reported to count as having moved. A phone standing still reports positions
// that wander by tens of metres, so a smaller radius would rarely see a vehicle
// as stopped.
const stoppedRadius = 50.0

// positionSpacing and positionHistorySize bound what is kept per vehicle: a
// sample about every positionSpacing and at most positionHistorySize of them,
// however often the vehicle reports. Spacing the samples out lets the history
// reach back minutes rather than over the last few reports, so a stopped
// vehicle whose GPS creeps away from where it stopped is still seen as
// stopped since the oldest sample it is near.
const (
	positionSpacing     = 30 * time.Second
	positionHistorySize = 16
)

// The kinds of alert, as the API names them.
const (
	alertQuiet     = "quiet"
	alertNotMoving = "not_moving"
)

// alertSettings are the thresholds, read from ALERT_QUIET_AFTER and
// ALERT_STOPPED_AFTER.
type alertSettings struct {
	// quietAfter is how long a vehicle on an active trip may go without a
	// report before it has gone quiet.
	quietAfter time.Duration
	// stoppedAfter is how long a vehicle on an active trip that is still
	// reporting may stay within stoppedRadius before it is not moving.
	stoppedAfter time.Duration
}

// alertEntry is one alert: a vehicle on an active trip that has gone quiet or
// is not moving, since when, and the trip.
type alertEntry struct {
	Kind string `json:"kind"`
	// Since is the vehicle's last report for a quiet alert, and when it came
	// within stoppedRadius of where it is now for a not-moving one.
	Since           time.Time   `json:"since"`
	DurationSeconds int64       `json:"duration_seconds"`
	Trip            TripSummary `json:"trip"`
}

type alertListResponse struct {
	Count  int          `json:"count"`
	Alerts []alertEntry `json:"alerts"`
}

// positionSample is one accepted report: when the server accepted it and
// where it put the vehicle.
type positionSample struct {
	at  time.Time
	pos rider.LatLon
}

// alertWatcher keeps what the alerts need that the tracker does not: when each
// vehicle last reported, and where it has been lately. The tracker keeps only
// a vehicle's latest position, and forgets the vehicle STALENESS_THRESHOLD
// after its last report, so it can tell neither whether a vehicle has moved
// nor how long one has been quiet.
type alertWatcher struct {
	trips    TripLister
	settings alertSettings
	// watchingSince is when the watcher started. It has seen no report from
	// before then, so it counts a quiet vehicle from then at the earliest.
	watchingSince time.Time

	mu sync.Mutex
	// history holds each vehicle's samples, oldest first. A vehicle with no
	// active trip loses its history the next time alerts are worked out.
	history map[string][]positionSample
}

// newAlertWatcher makes a watcher that reads the active trips from trips.
func newAlertWatcher(trips TripLister, settings alertSettings) *alertWatcher {
	return &alertWatcher{
		trips:         trips,
		settings:      settings,
		watchingSince: time.Now(),
		history:       make(map[string][]positionSample),
	}
}

// record adds a report the server accepted at at to its vehicle's history. The
// newest sample is always the latest report, and it stays in the history once
// it is positionSpacing newer than the sample before it, so the history holds
// a sample about every positionSpacing. A report older than the newest sample,
// one that lost a race with a later report from the same vehicle, is dropped.
func (w *alertWatcher) record(vehicleID string, pos rider.LatLon, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	sample := positionSample{at: at, pos: pos}
	samples := w.history[vehicleID]
	n := len(samples)
	switch {
	case n > 0 && at.Before(samples[n-1].at):
		return
	case n >= 2 && at.Sub(samples[n-2].at) < positionSpacing:
		samples[n-1] = sample
		return
	case n == positionHistorySize:
		samples = makeRoom(samples, sample)
	}
	w.history[vehicleID] = append(samples, sample)
}

// makeRoom drops one sample from a full history before next is added. It drops
// the oldest, unless next is within stoppedRadius of every sample: the vehicle
// has then not moved since the oldest, which is the start of its stop, so the
// second oldest goes instead and the stop keeps its true length.
func makeRoom(samples []positionSample, next positionSample) []positionSample {
	drop := 0
	if allWithin(samples, next.pos) {
		drop = 1
	}
	return append(samples[:drop], samples[drop+1:]...)
}

func allWithin(samples []positionSample, pos rider.LatLon) bool {
	for _, s := range samples {
		if rider.Distance(s.pos, pos) >= stoppedRadius {
			return false
		}
	}
	return true
}

// stillSince is when the vehicle came within stoppedRadius of its newest sample
// and stayed there: the oldest of the samples that, counting back from the
// newest, are all within stoppedRadius of it. samples must not be empty.
func stillSince(samples []positionSample) time.Time {
	newest := samples[len(samples)-1].pos
	i := len(samples) - 1
	for i > 0 && rider.Distance(samples[i-1].pos, newest) < stoppedRadius {
		i--
	}
	return samples[i].at
}

// alerts works out the alerts at now for active, which must be every active
// trip, longest-running first. It also drops the history of every vehicle
// with no active trip, so a trip that has ended leaves nothing behind.
func (w *alertWatcher) alerts(active []TripSummary, now time.Time) []alertEntry {
	w.mu.Lock()
	defer w.mu.Unlock()

	onTrip := make(map[string]struct{}, len(active))
	alerts := make([]alertEntry, 0)
	for _, trip := range active {
		onTrip[trip.VehicleID] = struct{}{}
		if alert, ok := w.alertFor(trip, now); ok {
			alerts = append(alerts, alert)
		}
	}
	for vehicleID := range w.history {
		if _, ok := onTrip[vehicleID]; !ok {
			delete(w.history, vehicleID)
		}
	}

	sort.Slice(alerts, func(i, j int) bool {
		if !alerts[i].Since.Equal(alerts[j].Since) {
			return alerts[i].Since.Before(alerts[j].Since)
		}
		return alerts[i].Trip.VehicleID < alerts[j].Trip.VehicleID
	})
	return alerts
}

// alertFor is the alert for one active trip at now, if it has one. A vehicle
// that is quiet is not also checked for moving: with no report to go on, the
// last position says nothing about now.
func (w *alertWatcher) alertFor(trip TripSummary, now time.Time) (alertEntry, bool) {
	samples := w.history[trip.VehicleID]
	// Samples from before the trip started belong to the vehicle's last trip.
	first := sort.Search(len(samples), func(i int) bool { return !samples[i].at.Before(trip.StartTime) })
	samples = samples[first:]

	// With no report on this trip yet, the vehicle is quiet from the trip's
	// start, or from when the watcher started if that is later.
	heard := trip.StartTime
	if w.watchingSince.After(heard) {
		heard = w.watchingSince
	}
	if n := len(samples); n > 0 && samples[n-1].at.After(heard) {
		heard = samples[n-1].at
	}
	if now.Sub(heard) > w.settings.quietAfter {
		return newAlert(alertQuiet, heard, trip, now), true
	}

	if len(samples) == 0 {
		return alertEntry{}, false
	}
	if since := stillSince(samples); now.Sub(since) >= w.settings.stoppedAfter {
		return newAlert(alertNotMoving, since, trip, now), true
	}
	return alertEntry{}, false
}

func newAlert(kind string, since time.Time, trip TripSummary, now time.Time) alertEntry {
	return alertEntry{
		Kind:            kind,
		Since:           since.UTC(),
		DurationSeconds: int64(now.Sub(since) / time.Second),
		Trip:            trip,
	}
}

// list works out the alerts at now.
func (w *alertWatcher) list(ctx context.Context, now time.Time) ([]alertEntry, error) {
	// Every active trip in one read: idx_trips_one_active_per_vehicle allows
	// at most one per vehicle, so the fleet bounds the list.
	active, err := w.trips.ListTrips(ctx, TripFilter{Status: "active", Limit: math.MaxInt32})
	if err != nil {
		return nil, fmt.Errorf("list active trips: %w", err)
	}
	return w.alerts(active, now), nil
}

// alertFeed is the LocationStore that POST /api/v1/locations saves through, so
// the watcher sees every report the server accepts. A report is recorded only
// once it is saved: one refused or not saved was answered with an error and
// moved nothing.
type alertFeed struct {
	LocationStore
	alerts *alertWatcher
}

func (f alertFeed) SaveLocation(ctx context.Context, loc *LocationReport) error {
	if err := f.LocationStore.SaveLocation(ctx, loc); err != nil {
		return err
	}
	f.alerts.record(loc.VehicleID, rider.LatLon{Lat: loc.Latitude, Lon: loc.Longitude}, time.Now())
	return nil
}

// handleListAlerts returns the current alerts, the same list the admin Alerts
// page shows.
func handleListAlerts(alerts *alertWatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := alerts.list(r.Context(), time.Now())
		if err != nil {
			slog.Error("failed to list alerts", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list alerts"})
			return
		}
		writeJSON(w, http.StatusOK, alertListResponse{Count: len(list), Alerts: list})
	}
}
