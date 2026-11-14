package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	// defaultReportOnTimeEarly and defaultReportOnTimeLate are the on-time
	// window: a stop passed from one minute early to five minutes late is on
	// time. They are the cut-offs the Android driver app shows its driver.
	defaultReportOnTimeEarly = time.Minute
	defaultReportOnTimeLate  = 5 * time.Minute

	// reportDateLayout is the date format of the API's date parameter and the
	// page's date picker.
	reportDateLayout = "2006-01-02"
	// gtfsDateLayout is the GTFS calendar date format the schedule index
	// takes.
	gtfsDateLayout = "20060102"

	// reportSettleAfter is how long after a day's last scheduled arrival its
	// report is kept rather than worked out again: time for a run that is
	// late to finish and for its phone to send what it has.
	reportSettleAfter = time.Hour
	// reportCacheSize bounds the reports kept in memory, a month of days.
	reportCacheSize = 31
	// maxRunGap is the furthest from a scheduled run of its GTFS trip that a
	// driver may start a trip for it to count as that run. Half a day keeps a
	// trip run every day on the nearest day's run, wherever its driver
	// started it, and a day the trip does not run off the day after.
	maxRunGap = 12 * time.Hour
)

var (
	errNoSchedule    = errors.New("schedule data unavailable")
	errBadReportDate = errors.New("date must be YYYY-MM-DD")
)

// reportSettings are the thresholds a service report judges runs by.
type reportSettings struct {
	onTimeEarly time.Duration
	onTimeLate  time.Duration
	// maxShapeDistance is the off-route distance (RIDER_MAX_SHAPE_DISTANCE)
	// tripLocator places reports with.
	maxShapeDistance float64
}

// envNonNegativeDurationOrDefault reads a duration that may be zero but not
// negative: a zero on-time margin is a strict agency's choice, while a
// negative one would call a vehicle on time only when it is off schedule.
func envNonNegativeDurationOrDefault(key string, fallback time.Duration) time.Duration {
	d := envDurationOrDefault(key, fallback)
	if d < 0 {
		slog.Warn("duration must not be negative, using default", "key", key, "value", d, "default", fallback)
		return fallback
	}
	return d
}

// serviceReportStore is what a service report reads: the trips started around
// the day and each one's trail.
type serviceReportStore interface {
	TripsStartedBetweenLister
	TripLocationLister
}

// serviceReport is one service day's report: for each route with service
// that day, the trips scheduled, how many ran, and how close to schedule.
type serviceReport struct {
	Date         string        `json:"date"`
	Timezone     string        `json:"timezone"`
	OnTimeEarlyS int           `json:"on_time_early_s"`
	OnTimeLateS  int           `json:"on_time_late_s"`
	Routes       []routeReport `json:"routes"`

	// serviceEnds is the day's last scheduled arrival.
	serviceEnds time.Time
	// location is the agency timezone the page shows times in.
	location *time.Location
}

// routeReport is one route's row of a service report. The stop counts are
// over the timepoints of the trips that ran.
type routeReport struct {
	RouteID        string       `json:"route_id"`
	ShortName      string       `json:"short_name"`
	LongName       string       `json:"long_name"`
	TripsScheduled int          `json:"trips_scheduled"`
	TripsRun       int          `json:"trips_run"`
	StopsOnTime    int          `json:"stops_on_time"`
	StopsEarly     int          `json:"stops_early"`
	StopsLate      int          `json:"stops_late"`
	StopsUnknown   int          `json:"stops_unknown"`
	OnTimePercent  *float64     `json:"on_time_percent"`
	UnknownPercent *float64     `json:"unknown_percent"`
	Trips          []tripReport `json:"trips"`
}

// tripReport is one scheduled trip of a route, run or not. A trip run more
// than once, say by a second vehicle after a breakdown, is judged on every
// run's points together.
type tripReport struct {
	GtfsTripID          string    `json:"gtfs_trip_id"`
	Headsign            string    `json:"headsign"`
	ScheduledDeparture  time.Time `json:"scheduled_departure"`
	ScheduledArrival    time.Time `json:"scheduled_arrival"`
	Runs                []tripRun `json:"runs"`
	DepartureDeviationS *int      `json:"departure_deviation_s"`
	ArrivalDeviationS   *int      `json:"arrival_deviation_s"`
	StopsOnTime         int       `json:"stops_on_time"`
	StopsEarly          int       `json:"stops_early"`
	StopsLate           int       `json:"stops_late"`
	StopsUnknown        int       `json:"stops_unknown"`
	OnTimePercent       *float64  `json:"on_time_percent"`
	UnknownPercent      *float64  `json:"unknown_percent"`
}

// tripRun is a driver's trip that ran a scheduled trip.
type tripRun struct {
	ID           int64  `json:"id"`
	VehicleID    string `json:"vehicle_id"`
	VehicleLabel string `json:"vehicle_label"`
	DriverName   string `json:"driver_name"`
}

// serviceReporter works out service reports on request and keeps those of
// days whose service is over, which no longer change.
type serviceReporter struct {
	store    serviceReportStore
	index    func() *rider.Index
	settings reportSettings
	now      func() time.Time

	mu sync.Mutex
	// schedule is the index the kept reports were worked out against. A
	// refresh puts a new index in place even when the feed has not changed,
	// and the cache then starts again: reports kept against the old index
	// are never asked for again, and each would keep that whole schedule in
	// memory.
	schedule *rider.Index
	cache    map[string]*serviceReport // by service date
	order    []string                  // oldest first
}

func newServiceReporter(store serviceReportStore, index func() *rider.Index, settings reportSettings) *serviceReporter {
	return &serviceReporter{
		store:    store,
		index:    index,
		settings: settings,
		now:      time.Now,
		cache:    make(map[string]*serviceReport),
	}
}

// report returns the report for date, YYYY-MM-DD, or for yesterday in the
// agency's timezone when date is empty.
func (r *serviceReporter) report(ctx context.Context, date string) (*serviceReport, error) {
	ix := r.index()
	if ix == nil {
		return nil, errNoSchedule
	}
	day, err := r.reportDay(ix, date)
	if err != nil {
		return nil, err
	}
	serviceDate := day.Format(gtfsDateLayout)
	if cached, ok := r.cached(ix, serviceDate); ok {
		return cached, nil
	}

	rep, err := r.compute(ctx, ix, day)
	if err != nil {
		return nil, err
	}
	if r.settled(ix, serviceDate, rep) {
		r.keep(ix, serviceDate, rep)
	}
	return rep, nil
}

// reportDay parses date, or picks yesterday in the agency's timezone when it
// is empty. The day is midnight UTC on that date, a calendar date and not an
// instant.
func (r *serviceReporter) reportDay(ix *rider.Index, date string) (time.Time, error) {
	if date == "" {
		y, m, d := r.now().In(ix.Timezone()).AddDate(0, 0, -1).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
	}
	day, err := time.Parse(reportDateLayout, date)
	if err != nil {
		return time.Time{}, errBadReportDate
	}
	// The days either side are read too, and all of them must stay inside
	// the four-digit years a GTFS date can hold.
	if day.Year() < 1 || day.Year() > 9998 {
		return time.Time{}, errBadReportDate
	}
	return day, nil
}

// settled reports whether the report for date can be kept: the day is before
// today's service date and its last scheduled arrival was over
// reportSettleAfter ago. Today is always worked out again, and so is a day
// whose service ran past midnight until it is over.
func (r *serviceReporter) settled(ix *rider.Index, date string, rep *serviceReport) bool {
	now := r.now()
	return date < ix.ServiceDate(now) && !now.Before(rep.serviceEnds.Add(reportSettleAfter))
}

// cached returns the report kept for date. Asking with a schedule other than
// the one the kept reports were worked out against empties the cache first.
func (r *serviceReporter) cached(ix *rider.Index, date string) (*serviceReport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.schedule != ix {
		r.schedule = ix
		clear(r.cache)
		r.order = nil
	}
	rep, ok := r.cache[date]
	return rep, ok
}

// keep caches rep, dropping the oldest report kept once there are
// reportCacheSize. A report worked out against ix while a request with
// another schedule took the cache over is not kept.
func (r *serviceReporter) keep(ix *rider.Index, date string, rep *serviceReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.schedule != ix {
		return
	}
	if _, ok := r.cache[date]; ok {
		return
	}
	if len(r.order) == reportCacheSize {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
	r.cache[date] = rep
	r.order = append(r.order, date)
}

// compute works out the report for day. A driver's trip counts towards the
// day when the day's run of its GTFS trip is the run nearest to when it
// started, so the trips to read are those started from the day before to the
// day after.
func (r *serviceReporter) compute(ctx context.Context, ix *rider.Index, day time.Time) (*serviceReport, error) {
	date := day.Format(gtfsDateLayout)
	from, err := rider.ServiceDayStart(day.AddDate(0, 0, -1).Format(gtfsDateLayout), ix.Timezone())
	if err != nil {
		return nil, err
	}
	to, err := rider.ServiceDayStart(day.AddDate(0, 0, 2).Format(gtfsDateLayout), ix.Timezone())
	if err != nil {
		return nil, err
	}
	started, err := r.store.ListTripsStartedBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}

	runs := make(map[string][]TripSummary)
	for _, t := range started {
		trip, ok := ix.Trip(t.GtfsTripID)
		if !ok {
			continue
		}
		if runDate, ok := runServiceDate(ix, trip, t.StartTime); ok && runDate == date {
			runs[trip.ID] = append(runs[trip.ID], t)
		}
	}

	points := make(map[string][]LocationPoint, len(runs))
	for tripID, tripRuns := range runs {
		for _, t := range tripRuns {
			trail, err := r.store.ListTripLocations(ctx, t.ID)
			if err != nil {
				return nil, fmt.Errorf("trail of trip %d: %w", t.ID, err)
			}
			points[tripID] = append(points[tripID], trail...)
		}
	}
	return buildServiceReport(ix, day, runs, points, r.settings)
}

// buildServiceReport is a day's report from the driver trips that ran its
// scheduled trips and their points, both keyed by GTFS trip id.
func buildServiceReport(ix *rider.Index, day time.Time, runs map[string][]TripSummary, points map[string][]LocationPoint, s reportSettings) (*serviceReport, error) {
	date := day.Format(gtfsDateLayout)
	dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
	if err != nil {
		return nil, err
	}
	rep := &serviceReport{
		Date:         day.Format(reportDateLayout),
		Timezone:     ix.Timezone().String(),
		OnTimeEarlyS: int(s.onTimeEarly / time.Second),
		OnTimeLateS:  int(s.onTimeLate / time.Second),
		Routes:       make([]routeReport, 0),
		serviceEnds:  dayStart,
		location:     ix.Timezone(),
	}

	for _, route := range ix.Routes() {
		scheduled := ix.TripsOnRoute(route.ID, date)
		if len(scheduled) == 0 {
			continue
		}
		row := routeReport{
			RouteID:        route.ID,
			ShortName:      route.ShortName,
			LongName:       route.LongName,
			TripsScheduled: len(scheduled),
			Trips:          make([]tripReport, 0, len(scheduled)),
		}
		var counts stopCounts
		for _, trip := range scheduled {
			tr := newTripReport(trip, dayStart, runs[trip.ID])
			if ends := tr.ScheduledArrival; ends.After(rep.serviceEnds) {
				rep.serviceEnds = ends
			}
			if len(tr.Runs) > 0 {
				row.TripsRun++
				eval := evaluateRun(trip, dayStart, points[trip.ID], s)
				tr.setEvaluation(eval)
				counts.merge(eval.counts)
			}
			row.Trips = append(row.Trips, tr)
		}
		row.StopsOnTime, row.StopsEarly, row.StopsLate, row.StopsUnknown = counts.onTime, counts.early, counts.late, counts.unknown
		row.OnTimePercent, row.UnknownPercent = counts.onTimePercent(), counts.unknownPercent()
		rep.Routes = append(rep.Routes, row)
	}
	return rep, nil
}

func newTripReport(trip *rider.TripInfo, dayStart time.Time, runs []TripSummary) tripReport {
	first, last := trip.StopTimes[0], trip.StopTimes[len(trip.StopTimes)-1]
	tr := tripReport{
		GtfsTripID:         trip.ID,
		Headsign:           trip.Headsign,
		ScheduledDeparture: dayStart.Add(first.Departure),
		ScheduledArrival:   dayStart.Add(last.Arrival),
		Runs:               make([]tripRun, 0, len(runs)),
	}
	for _, t := range runs {
		tr.Runs = append(tr.Runs, tripRun{ID: t.ID, VehicleID: t.VehicleID, VehicleLabel: t.VehicleLabel, DriverName: t.DriverName})
	}
	return tr
}

func (tr *tripReport) setEvaluation(eval runEvaluation) {
	tr.DepartureDeviationS = durationSeconds(eval.departure)
	tr.ArrivalDeviationS = durationSeconds(eval.arrival)
	tr.StopsOnTime, tr.StopsEarly, tr.StopsLate, tr.StopsUnknown = eval.counts.onTime, eval.counts.early, eval.counts.late, eval.counts.unknown
	tr.OnTimePercent, tr.UnknownPercent = eval.counts.onTimePercent(), eval.counts.unknownPercent()
}

func durationSeconds(d *time.Duration) *int {
	if d == nil {
		return nil
	}
	s := int(*d / time.Second)
	return &s
}

// runServiceDate is the service date, YYYYMMDD, of the run of trip that a
// driver who started it at start was driving: of the days around start's
// calendar day that the trip runs on, the one whose run, from first departure
// to last arrival, is nearest start. A run after midnight on a service day
// that began the evening before is one; so is a driver starting the first
// run of the day a little before midnight. ok is false when no run of the
// trip is within maxRunGap of start: a trip driven on a holiday it does not
// run on is not the next day's run.
func runServiceDate(ix *rider.Index, trip *rider.TripInfo, start time.Time) (string, bool) {
	y, m, d := start.In(ix.Timezone()).Date()
	calendarDay := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)

	best, bestGap := "", time.Duration(0)
	for _, offset := range []int{-1, 0, 1} {
		date := calendarDay.AddDate(0, 0, offset).Format(gtfsDateLayout)
		if !ix.ActiveOn(trip, date) {
			continue
		}
		dayStart, err := rider.ServiceDayStart(date, ix.Timezone())
		if err != nil {
			continue
		}
		if gap := scheduledRunGap(trip, dayStart, start); gap <= maxRunGap && (best == "" || gap < bestGap) {
			best, bestGap = date, gap
		}
	}
	return best, best != ""
}

// scheduledRunGap is how far t is from trip's run on the service day starting
// at dayStart: zero during it, otherwise the time to its first departure or
// since its last arrival.
func scheduledRunGap(trip *rider.TripInfo, dayStart, t time.Time) time.Duration {
	first := dayStart.Add(trip.StopTimes[0].Departure)
	last := dayStart.Add(trip.StopTimes[len(trip.StopTimes)-1].Arrival)
	switch {
	case t.Before(first):
		return first.Sub(t)
	case t.After(last):
		return t.Sub(last)
	}
	return 0
}
