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
// vehicle off its route for five is not doing that. A trip that leaves five
// minutes after it was due is late by the Reports page's default on-time
// window (REPORT_ON_TIME_LATE).
const (
	defaultAlertQuietAfter      = 3 * time.Minute
	defaultAlertStoppedAfter    = 10 * time.Minute
	defaultAlertOffRouteAfter   = 5 * time.Minute
	defaultAlertNotStartedAfter = 5 * time.Minute
)

// notStartedLookback is how far back list reads the trips started, to find
// the trip each block was run on last. A day covers any block's day, split
// shifts and trips past midnight included.
const notStartedLookback = 24 * time.Hour

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
	alertQuiet      = "quiet"
	alertNotMoving  = "not_moving"
	alertOffRoute   = "off_route"
	alertNotStarted = "not_started"
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
	// notStartedAfter is how long after it was due to leave the next trip of
	// a block may go unstarted before the vehicle working the block is late
	// to start it.
	notStartedAfter time.Duration
}

// alertSettingsFromEnv reads the thresholds from ALERT_QUIET_AFTER,
// ALERT_STOPPED_AFTER, ALERT_OFF_ROUTE_DISTANCE, ALERT_OFF_ROUTE_AFTER and
// ALERT_NOT_STARTED_AFTER. The
// off-route distance defaults to maxShapeDistance, RIDER_MAX_SHAPE_DISTANCE,
// which is where the driver apps already tell the driver they are off route.
func alertSettingsFromEnv(maxShapeDistance float64) alertSettings {
	return alertSettings{
		quietAfter:       envPositiveDurationOrDefault("ALERT_QUIET_AFTER", defaultAlertQuietAfter),
		stoppedAfter:     envPositiveDurationOrDefault("ALERT_STOPPED_AFTER", defaultAlertStoppedAfter),
		offRouteDistance: envPositiveFloatOrDefault("ALERT_OFF_ROUTE_DISTANCE", maxShapeDistance),
		offRouteAfter:    envPositiveDurationOrDefault("ALERT_OFF_ROUTE_AFTER", defaultAlertOffRouteAfter),
		notStartedAfter:  envPositiveDurationOrDefault("ALERT_NOT_STARTED_AFTER", defaultAlertNotStartedAfter),
	}
}

// alertEntry is one alert: a vehicle on an active trip that has gone quiet, is
// off route or is not moving, or a vehicle working a block that has not
// started the block's next trip, since when, and the trip.
type alertEntry struct {
	Kind string `json:"kind"`
	// Since is the vehicle's last report for a quiet alert, its first report
	// off route for an off-route one, when it came within stoppedRadius of
	// where it is now for a not-moving one, and when the next trip was due to
	// leave for a not-started one.
	Since           time.Time `json:"since"`
	DurationSeconds int64     `json:"duration_seconds"`
	// Trip is the trip running, or for a not-started alert the trip the
	// vehicle ran last.
	Trip TripSummary `json:"trip"`
	// NextTrip is the trip a not-started alert is about, nil for the others.
	NextTrip *alertNextTrip `json:"next_trip,omitempty"`
}

// alertNextTrip is the scheduled trip a vehicle working a block has not
// started.
type alertNextTrip struct {
	GtfsTripID string `json:"gtfs_trip_id"`
	RouteID    string `json:"route_id"`
	BlockID    string `json:"block_id"`
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

// routeState is what a vehicle's reports have said about where it is against
// its trip's shape.
type routeState struct {
	// tripStart is when the trip whose route the vehicle has joined started:
	// a report near the shape sets it, and zero means none has yet. Until then
	// the vehicle is still on its way to the route, from a depot or a layover
	// spot beside it, and a report away from the shape is not off route. The
	// start tells one run of a GTFS trip from the next, so a vehicle on a
	// headway route, which starts the same trip_id over and over, joins each
	// run's route anew.
	tripStart time.Time
	// offSince and offLast are the first and the latest report off the shape
	// since the last one near it, both zero when there is none.
	offSince, offLast time.Time
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
	// routes holds each vehicle's routeState for its latest trip. A vehicle
	// with no active trip loses it with its history.
	routes map[string]routeState
	// schedule is where the trips' shapes and blocks come from, nil until
	// watchRoutes.
	schedule func() *rider.Index
	// started lists the trips started lately, nil until watchBlocks.
	started TripsStartedBetweenLister
	// webhook posts the alerts to ALERT_WEBHOOK_URL, for the Alerts page to
	// show how its posts went. It is nil when the setting is unset, and set
	// once by startAlertJobs before any request comes.
	webhook *alertWebhook
}

// newAlertWatcher makes a watcher that reads the active trips from trips.
func newAlertWatcher(trips TripLister, settings alertSettings) *alertWatcher {
	return &alertWatcher{
		trips:         trips,
		settings:      settings,
		watchingSince: time.Now(),
		history:       make(map[string][]positionSample),
		routes:        make(map[string]routeState),
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

// watchBlocks makes the watcher look for blocks whose next trip has not
// started, reading the trips started lately from started. Until then it does
// not.
func (w *alertWatcher) watchBlocks(started TripsStartedBetweenLister) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.started = started
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

// recordRoute updates the vehicle's routeState with a report accepted at at. A
// report that cannot tell changes nothing. A report near the shape starts the
// state over for its own trip, so a vehicle that ends one trip off its route
// and starts the next carries none of it over.
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
	state := w.routes[loc.VehicleID]
	switch {
	case side == sideNear:
		state = routeState{tripStart: loc.TripStartedAt}
	case state.offSince.IsZero():
		state.offSince, state.offLast = at, at
	default:
		state.offLast = at
	}
	w.routes[loc.VehicleID] = state
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
	for vehicleID := range w.routes {
		if _, ok := onTrip[vehicleID]; !ok {
			delete(w.routes, vehicleID)
		}
	}

	sortAlerts(alerts)
	return alerts
}

// sortAlerts puts alerts longest-running first.
func sortAlerts(alerts []alertEntry) {
	sort.Slice(alerts, func(i, j int) bool {
		if !alerts[i].Since.Equal(alerts[j].Since) {
			return alerts[i].Since.Before(alerts[j].Since)
		}
		return alerts[i].Trip.VehicleID < alerts[j].Trip.VehicleID
	})
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

	// Only a vehicle that has joined this trip's route can be off it: a state
	// joined on its last trip, or not joined at all, says nothing about this
	// one. As for a stop, only reported time counts.
	state := w.routes[trip.VehicleID]
	if state.tripStart.Equal(trip.StartTime) && state.offLast.Sub(state.offSince) > w.settings.offRouteAfter {
		return newAlert(alertOffRoute, state.offSince, trip, now), true
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
	alerts := w.alerts(active, now)

	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if started == nil {
		return alerts, nil
	}
	recent, err := started.ListTripsStartedBetween(ctx, now.Add(-notStartedLookback), now)
	if err != nil {
		return nil, fmt.Errorf("list trips started: %w", err)
	}
	alerts = append(alerts, w.notStarted(active, recent, now)...)
	sortAlerts(alerts)
	return alerts, nil
}

// notStarted works out the not-started alerts at now. recent must be every
// trip started over the last notStartedLookback and active every trip
// running. A block is worked by the vehicle that ran its trip latest in the
// schedule, while that is the vehicle's own latest trip and it has nothing
// running. The block's next trip that day is late to start once
// notStartedAfter has passed since it was due to leave, and stays so until it
// is due to arrive: by then it can no longer run, and with nothing to
// acknowledge an alert by, a block dropped on purpose would otherwise keep
// one all day.
//
// Only a vehicle that has run a trip on a block today can raise one, so in a
// pilot where few vehicles carry the app, the blocks nobody is working stay
// quiet.
func (w *alertWatcher) notStarted(active, recent []TripSummary, now time.Time) []alertEntry {
	w.mu.Lock()
	schedule := w.schedule
	w.mu.Unlock()
	if schedule == nil {
		return nil
	}
	ix := schedule()
	if ix == nil {
		return nil
	}

	busy := make(map[string]struct{}, len(active))
	for _, trip := range active {
		busy[trip.VehicleID] = struct{}{}
	}
	type blockDay struct{ block, serviceDate string }
	type blockRun struct {
		trip TripSummary
		info *rider.TripInfo
	}
	latestOfVehicle := make(map[string]TripSummary)
	latestOfBlock := make(map[blockDay]blockRun)
	for _, trip := range recent {
		if latest, ok := latestOfVehicle[trip.VehicleID]; !ok || trip.StartTime.After(latest.StartTime) {
			latestOfVehicle[trip.VehicleID] = trip
		}
		info, ok := ix.Trip(trip.GtfsTripID)
		// A headway trip's runs share one trip_id and leave at no set time.
		if !ok || info.BlockID == "" || info.FrequencyBased {
			continue
		}
		key := blockDay{info.BlockID, ix.ServiceDateFor(info, trip.StartTime)}
		// Of two runs of one trip, after an end by mistake, a relief mid-trip
		// or a breakdown, the one started later has the block. Compared on
		// the start rather than relying on recent's order.
		run, ok := latestOfBlock[key]
		if !ok || departs(info) > departs(run.info) ||
			(departs(info) == departs(run.info) && trip.StartTime.After(run.trip.StartTime)) {
			latestOfBlock[key] = blockRun{trip, info}
		}
	}

	alerts := make([]alertEntry, 0)
	var blocks map[string][]*rider.TripInfo
	for key, run := range latestOfBlock {
		vehicleID := run.trip.VehicleID
		if _, ok := busy[vehicleID]; ok || latestOfVehicle[vehicleID].ID != run.trip.ID {
			continue
		}
		if blocks == nil {
			blocks = tripsByBlock(ix)
		}
		next := nextOnBlock(ix, blocks[key.block], key.serviceDate, departs(run.info))
		if next == nil {
			continue
		}
		dayStart, err := rider.ServiceDayStart(key.serviceDate, ix.Timezone())
		if err != nil {
			continue
		}
		due := dayStart.Add(departs(next))
		if now.Sub(due) > w.settings.notStartedAfter && now.Before(dayStart.Add(arrives(next))) {
			alert := newAlert(alertNotStarted, due, run.trip, now)
			alert.NextTrip = &alertNextTrip{GtfsTripID: next.ID, RouteID: next.RouteID, BlockID: next.BlockID}
			alerts = append(alerts, alert)
		}
	}
	return alerts
}

// tripsByBlock is the schedule's trips by block_id.
func tripsByBlock(ix *rider.Index) map[string][]*rider.TripInfo {
	blocks := make(map[string][]*rider.TripInfo)
	for _, id := range ix.TripIDs() {
		if trip, ok := ix.Trip(id); ok {
			blocks[trip.BlockID] = append(blocks[trip.BlockID], trip)
		}
	}
	return blocks
}

// nextOnBlock is the trip of block that runs on serviceDate and leaves first
// after after, nil when there is none or it is a headway trip, which leaves at
// no set time.
func nextOnBlock(ix *rider.Index, block []*rider.TripInfo, serviceDate string, after time.Duration) *rider.TripInfo {
	var next *rider.TripInfo
	for _, trip := range block {
		if departs(trip) > after && ix.ActiveOn(trip, serviceDate) && (next == nil || departs(trip) < departs(next)) {
			next = trip
		}
	}
	if next != nil && next.FrequencyBased {
		return nil
	}
	return next
}

// departs and arrives are when a trip leaves its first stop and reaches its
// last, from the start of its service day.
func departs(trip *rider.TripInfo) time.Duration { return trip.StopTimes[0].Departure }

func arrives(trip *rider.TripInfo) time.Duration {
	return trip.StopTimes[len(trip.StopTimes)-1].Arrival
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
