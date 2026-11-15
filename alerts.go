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
// drops off the map. Going round a blocked street takes a minute or two, so a
// vehicle off its route for five is not doing that.
const (
	defaultAlertQuietAfter    = 3 * time.Minute
	defaultAlertStoppedAfter  = 10 * time.Minute
	defaultAlertOffRouteAfter = 5 * time.Minute
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
	alertOffRoute  = "off_route"
)

// alertSettings are the thresholds, read by alertSettingsFromEnv.
type alertSettings struct {
	// quietAfter is how long a vehicle on an active trip may go without a
	// report before it has gone quiet.
	quietAfter time.Duration
	// stoppedAfter is how long a vehicle on an active trip that is still
	// reporting may stay within stoppedRadius before it is not moving.
	stoppedAfter time.Duration
	// offRouteDistance is how far, in metres, a report has to put a vehicle
	// from its trip's shape, beyond the fix's own accuracy, to count as off
	// route.
	offRouteDistance float64
	// offRouteAfter is how long a vehicle on an active trip may keep
	// reporting from off its route before it is off route.
	offRouteAfter time.Duration
}

// alertSettingsFromEnv reads the thresholds from ALERT_QUIET_AFTER,
// ALERT_STOPPED_AFTER, ALERT_OFF_ROUTE_DISTANCE and ALERT_OFF_ROUTE_AFTER. The
// off-route distance defaults to maxShapeDistance, RIDER_MAX_SHAPE_DISTANCE,
// which is where the driver apps already tell the driver they are off route.
func alertSettingsFromEnv(maxShapeDistance float64) alertSettings {
	return alertSettings{
		quietAfter:       envPositiveDurationOrDefault("ALERT_QUIET_AFTER", defaultAlertQuietAfter),
		stoppedAfter:     envPositiveDurationOrDefault("ALERT_STOPPED_AFTER", defaultAlertStoppedAfter),
		offRouteDistance: envPositiveFloatOrDefault("ALERT_OFF_ROUTE_DISTANCE", maxShapeDistance),
		offRouteAfter:    envPositiveDurationOrDefault("ALERT_OFF_ROUTE_AFTER", defaultAlertOffRouteAfter),
	}
}

// alertEntry is one alert: a vehicle on an active trip that has gone quiet, is
// off route or is not moving, since when, and the trip.
type alertEntry struct {
	Kind string `json:"kind"`
	// Since is the vehicle's last report for a quiet alert, its first report
	// off route for an off-route one, and when it came within stoppedRadius of
	// where it is now for a not-moving one.
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

// offRouteSpell is a vehicle's run of reports off its trip's shape, with none
// near it in between.
type offRouteSpell struct {
	// tripID is the GTFS trip the reports named. A report off another trip's
	// shape starts a new spell.
	tripID string
	since  time.Time // the first report off the shape
	last   time.Time // the latest
}

// shapeSide is where a report puts its vehicle against the off-route distance.
type shapeSide int

const (
	// sideUnknown is a report that cannot tell: its trip has no shape to judge
	// against, or the fix is too coarse to say which side it is on.
	sideUnknown shapeSide = iota
	sideNear
	sideOff
)

// alertWatcher keeps what the alerts need that the tracker does not: when each
// vehicle last reported, where it has been lately, and how long it has been off
// its route. The tracker keeps only a vehicle's latest position, and forgets
// the vehicle STALENESS_THRESHOLD after its last report, so it can tell neither
// whether a vehicle has moved nor how long one has been quiet.
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
	// offRoute holds each vehicle's current spell off its trip's shape, which
	// it loses with its history.
	offRoute map[string]offRouteSpell
	// schedule is where the trips' shapes come from, nil until watchRoutes.
	schedule func() *rider.Index
}

// newAlertWatcher makes a watcher that reads the active trips from trips.
func newAlertWatcher(trips TripLister, settings alertSettings) *alertWatcher {
	return &alertWatcher{
		trips:         trips,
		settings:      settings,
		watchingSince: time.Now(),
		history:       make(map[string][]positionSample),
		offRoute:      make(map[string]offRouteSpell),
	}
}

// watchRoutes makes the watcher judge each report against its trip's shape in
// the schedule index returns, which the off-route alert needs. Until then no
// vehicle is off route.
func (w *alertWatcher) watchRoutes(index func() *rider.Index) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.schedule = index
}

// report adds a report the server accepted at at to what the alerts know of
// its vehicle.
func (w *alertWatcher) report(loc *LocationReport, at time.Time) {
	w.record(loc.VehicleID, rider.LatLon{Lat: loc.Latitude, Lon: loc.Longitude}, at)
	w.recordRoute(loc, at)
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

// recordRoute starts, extends or ends the vehicle's spell off route with a
// report accepted at at. A report that cannot tell leaves the spell as it is.
func (w *alertWatcher) recordRoute(loc *LocationReport, at time.Time) {
	w.mu.Lock()
	schedule := w.schedule
	w.mu.Unlock()
	// Judged outside the lock: the projection is a pass over every segment of
	// the shape.
	side := judgeReport(schedule, loc, w.settings.offRouteDistance)
	if side == sideUnknown {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	spell, ok := w.offRoute[loc.VehicleID]
	switch {
	case side == sideNear:
		delete(w.offRoute, loc.VehicleID)
	case ok && spell.tripID == loc.TripID:
		spell.last = at
		w.offRoute[loc.VehicleID] = spell
	default:
		w.offRoute[loc.VehicleID] = offRouteSpell{tripID: loc.TripID, since: at, last: at}
	}
}

// judgeReport is where loc puts its vehicle against limit, measured from the
// shape of the trip it names, found as tripLocator finds it. It cannot tell
// when no schedule is loaded, the report names no trip or one the schedule
// lacks, or the trip's shape was drawn through its stops, which cuts every
// corner between them.
func judgeReport(schedule func() *rider.Index, loc *LocationReport, limit float64) shapeSide {
	if schedule == nil {
		return sideUnknown
	}
	ix := schedule()
	if ix == nil {
		return sideUnknown
	}
	trip, ok := ix.Trip(loc.TripID)
	if !ok || trip.ShapeFromStops {
		return sideUnknown
	}
	// With no hint the nearest pass of a loop or an out-and-back counts: how
	// far a vehicle is from its route does not depend on which pass it is on.
	proj := trip.Shape.Project(rider.LatLon{Lat: loc.Latitude, Lon: loc.Longitude}, nil)
	accuracy := 0.0
	if loc.Accuracy != nil {
		accuracy = *loc.Accuracy
	}
	return judgeDistance(proj.DistanceToShape, accuracy, limit)
}

// judgeDistance is where a fix distance metres from the shape puts its vehicle
// against limit. A fix counts only when its accuracy circle lies wholly on one
// side: beyond limit plus its accuracy it is off, as the rider engine and the
// driver apps judge it, and within limit less its accuracy it is near. One in
// between says nothing, so a poor fix neither raises the alert nor clears it.
func judgeDistance(distance, accuracy, limit float64) shapeSide {
	accuracy = math.Max(accuracy, 0)
	switch {
	case distance > limit+accuracy:
		return sideOff
	case distance+accuracy <= limit:
		return sideNear
	}
	return sideUnknown
}

// alerts works out the alerts at now for active, which must be every active
// trip, longest-running first. It also drops what it keeps of every vehicle
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
	for vehicleID := range w.offRoute {
		if _, ok := onTrip[vehicleID]; !ok {
			delete(w.offRoute, vehicleID)
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
// that is quiet is not also checked for its route or for moving: with no
// report to go on, the last position says nothing about now. Off route comes
// before not moving, so a vehicle standing still away from its route keeps
// the one alert rather than changing kind when it has stood long enough.
func (w *alertWatcher) alertFor(trip TripSummary, now time.Time) (alertEntry, bool) {
	samples := w.history[trip.VehicleID]
	// Samples from before the trip started belong to the vehicle's last trip.
	first := sort.Search(len(samples), func(i int) bool { return !samples[i].at.Before(trip.StartTime) })
	onTrip := samples[first:]

	// With no report on this trip yet, the vehicle is quiet from the trip's
	// start, or from when the watcher started if that is later.
	heard := trip.StartTime
	if w.watchingSince.After(heard) {
		heard = w.watchingSince
	}
	if n := len(onTrip); n > 0 && onTrip[n-1].at.After(heard) {
		heard = onTrip[n-1].at
	}
	if now.Sub(heard) > w.settings.quietAfter {
		return newAlert(alertQuiet, heard, trip, now), true
	}

	// A spell begun before the trip counts from the trip's start. One on the
	// vehicle's last trip counts for nothing: a report on this trip either
	// starts a new spell or cannot tell, so it ended before this trip began.
	if spell, ok := w.offRoute[trip.VehicleID]; ok {
		since := spell.since
		if since.Before(trip.StartTime) {
			since = trip.StartTime
		}
		// As for a stop, only reported time counts.
		if spell.last.Sub(since) > w.settings.offRouteAfter {
			return newAlert(alertOffRoute, since, trip, now), true
		}
	}

	if len(onTrip) == 0 {
		return alertEntry{}, false
	}
	// The stop is worked out over the whole history and then counted from the
	// trip's start at the earliest. A vehicle that stays put from one trip into
	// the next keeps a sample from the last trip at the head of a full history
	// (makeRoom), and this trip's oldest samples are the ones that make way, so
	// this trip's samples alone would never reach back to its start.
	since := stillSince(samples)
	if since.Before(trip.StartTime) {
		since = trip.StartTime
	}
	// Only reported time counts towards a stop: the vehicle may have moved
	// off in the silence since its latest report.
	if onTrip[len(onTrip)-1].at.Sub(since) >= w.settings.stoppedAfter {
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
// the watcher sees every report the server saves. One refused or not saved was
// answered with an error and is not recorded. A saved report is recorded even
// when the tracker then refuses it because the vehicle was removed while it was
// being handled: the report did happen, and the watcher only uses it while the
// vehicle has an active trip.
type alertFeed struct {
	LocationStore
	alerts *alertWatcher
}

func (f alertFeed) SaveLocation(ctx context.Context, loc *LocationReport) error {
	if err := f.LocationStore.SaveLocation(ctx, loc); err != nil {
		return err
	}
	f.alerts.report(loc, time.Now())
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
