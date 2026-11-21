package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// dayTotalsVersion is the shape of the totals newDayTotals adds up. A kept
// day of another version is worked out again and replaced, as one judged by
// another on-time window is; a day kept before the totals had a version reads
// as 0. Raise it whenever what the totals count changes.
//
// 1: a trip in frequencies.txt counts the runs its windows schedule, by the
// hour each is due, and none of its stops is judged.
// 2: the gaps between vehicles on a route's frequencies.txt trips, by
// direction and hour, and the day's gaps by tenths of their scheduled gap.
const dayTotalsVersion = 2

// dayTotals is one service day as the Analytics page adds it up over a
// period: each route's counts by the hour its trips leave in, and when the
// trips that ran reached their last stop. A day older than yesterday is kept
// in the database in this form, so a period's past days are read rather than
// worked out again from every trail.
type dayTotals struct {
	// Date is the service date, YYYY-MM-DD.
	Date    string           `json:"-"`
	Version int              `json:"version"`
	Routes  []routeDayTotals `json:"routes"`
	// Arrivals counts the trips that ran by the whole minute, early (below
	// zero) or late, they reached their last stop in.
	Arrivals []arrivalMinute `json:"arrivals"`
	// ArrivalsLeftOut is the trips that ran but have no report at their last
	// stop.
	ArrivalsLeftOut int `json:"arrivals_left_out"`
	// GapRatios counts the gaps judged between vehicles by which tenth of
	// their scheduled gap each was, listing only the tenths with any.
	GapRatios []gapRatioCount `json:"gap_ratios"`
}

// routeDayTotals is one route with service on the day, in the schedule's
// order, named as the day's report named it.
type routeDayTotals struct {
	RouteID   string       `json:"route_id"`
	ShortName string       `json:"short_name"`
	LongName  string       `json:"long_name"`
	Hours     []hourTotals `json:"hours"`
	// Gaps is the gaps between vehicles on the route's frequencies.txt trips,
	// a direction at a time, as the day's report gives them.
	Gaps []directionGapTotals `json:"gaps"`
}

// hourTotals is a route's trips that leave in one hour of the service day,
// which can pass 23 for a trip after midnight. A trip in frequencies.txt is
// counted once for each of its runs, in the hour the run is due to leave.
type hourTotals struct {
	Hour      int `json:"hour"`
	Scheduled int `json:"scheduled"`
	Run       int `json:"run"`
	OnTime    int `json:"on_time"`
	Early     int `json:"early"`
	Late      int `json:"late"`
	Unknown   int `json:"unknown"`
}

// directionGapTotals is one direction of a route's frequencies.txt service,
// its gaps by the hour the later vehicle passed in.
type directionGapTotals struct {
	DirectionID *int            `json:"direction_id"`
	Headsign    string          `json:"headsign"`
	Hours       []gapHourTotals `json:"hours"`
}

// gapHourTotals is a direction's gaps whose later vehicle passed in one hour
// of the service day, which can pass 23 after midnight.
type gapHourTotals struct {
	Hour int `json:"hour"`
	gapCounts
}

// gapRatioCount is the gaps judged in one tenth of their scheduled gap, the
// last, gapRatioTop, holding 300% and over.
type gapRatioCount struct {
	Tenth int `json:"tenth"`
	Gaps  int `json:"gaps"`
}

// arrivalMinute is the trips that reached their last stop in one minute
// early or late, sorted by the on-time window the day was judged by.
type arrivalMinute struct {
	Minute int `json:"minute"`
	OnTime int `json:"on_time"`
	Early  int `json:"early"`
	Late   int `json:"late"`
}

func (h hourTotals) counts() reportCounts {
	return reportCounts{Scheduled: h.Scheduled, Run: h.Run, OnTime: h.OnTime, Early: h.Early, Late: h.Late, Unknown: h.Unknown}
}

func (r routeDayTotals) counts() reportCounts {
	var c reportCounts
	for _, h := range r.Hours {
		c.add(h.counts())
	}
	return c
}

func (d dayTotals) counts() reportCounts {
	var c reportCounts
	for _, r := range d.Routes {
		c.add(r.counts())
	}
	return c
}

// newDayTotals adds up rep, a day's service report worked out under s.
func newDayTotals(rep *serviceReport, s reportSettings) dayTotals {
	day := dayTotals{
		Date:      rep.Date,
		Version:   dayTotalsVersion,
		Routes:    make([]routeDayTotals, 0, len(rep.Routes)),
		Arrivals:  make([]arrivalMinute, 0),
		GapRatios: make([]gapRatioCount, 0),
	}
	arrivals := make(map[int]*arrivalMinute)
	var ratios [gapRatioTop + 1]int
	for _, route := range rep.Routes {
		hours := make(map[int]*hourTotals)
		inHour := func(at time.Time) *hourTotals {
			hour := serviceHour(rep, at)
			h, ok := hours[hour]
			if !ok {
				h = &hourTotals{Hour: hour}
				hours[hour] = h
			}
			return h
		}
		for _, trip := range route.Trips {
			if trip.Frequencies != nil {
				for _, slot := range trip.slots {
					h := inHour(slot.at)
					h.Scheduled++
					if slot.run {
						h.Run++
					}
				}
				continue
			}
			h := inHour(trip.ScheduledDeparture)
			h.Scheduled++
			// Only a trip that ran has stops judged, so these add nothing for
			// the others.
			h.OnTime += trip.StopsOnTime
			h.Early += trip.StopsEarly
			h.Late += trip.StopsLate
			h.Unknown += trip.StopsUnknown
			if len(trip.Runs) == 0 {
				continue
			}
			h.Run++
			if trip.ArrivalDeviationS == nil {
				day.ArrivalsLeftOut++
				continue
			}
			d := time.Duration(*trip.ArrivalDeviationS) * time.Second
			minute := int(math.Floor(d.Minutes()))
			a, ok := arrivals[minute]
			if !ok {
				a = &arrivalMinute{Minute: minute}
				arrivals[minute] = a
			}
			switch s.verdict(d) {
			case stopEarly:
				a.Early++
			case stopLate:
				a.Late++
			default:
				a.OnTime++
			}
		}
		totals := routeDayTotals{
			RouteID: route.RouteID, ShortName: route.ShortName, LongName: route.LongName,
			Hours: make([]hourTotals, 0, len(hours)), Gaps: make([]directionGapTotals, 0, len(route.Gaps)),
		}
		for _, h := range hours {
			totals.Hours = append(totals.Hours, *h)
		}
		slices.SortFunc(totals.Hours, func(a, b hourTotals) int { return cmp.Compare(a.Hour, b.Hour) })
		for _, d := range route.Gaps {
			totals.Gaps = append(totals.Gaps, directionGapTotals{DirectionID: d.DirectionID, Headsign: d.Headsign, Hours: d.hours})
			for tenth, n := range d.ratios {
				ratios[tenth] += n
			}
		}
		day.Routes = append(day.Routes, totals)
	}
	for tenth, n := range ratios {
		if n > 0 {
			day.GapRatios = append(day.GapRatios, gapRatioCount{Tenth: tenth, Gaps: n})
		}
	}
	for _, a := range arrivals {
		day.Arrivals = append(day.Arrivals, *a)
	}
	slices.SortFunc(day.Arrivals, func(a, b arrivalMinute) int { return cmp.Compare(a.Minute, b.Minute) })
	return day
}

// reportWarmInterval is how often the warmer looks for a schedule reload or a
// new day old enough to keep.
const reportWarmInterval = 5 * time.Minute

// reportWarmer works out in the background the past days the Analytics page
// opens on that the store does not keep yet: those of its longest period and
// the period before it, up to the day before yesterday. It does so at start,
// and again when the schedule is reloaded or the service date moves on, so the
// first look at a long period after an upgrade does not work dozens of days
// out on one request and run past WRITE_TIMEOUT. It asks for one day at a
// time through the reporter, so it holds at most one of the reporter's slots
// and a page always has the other.
type reportWarmer struct {
	reports *serviceReporter
	every   time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// startReportWarmer starts warming reports' days, checking every interval.
func startReportWarmer(reports *serviceReporter, every time.Duration) *reportWarmer {
	// A background job outlives any request, so it owns its context.
	ctx, cancel := context.WithCancel(context.Background())
	w := &reportWarmer{reports: reports, every: every, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	w.wg.Go(w.run)
	return w
}

// Stop cancels a day being worked out and waits for the warmer to exit. Safe
// to call more than once.
func (w *reportWarmer) Stop() {
	w.once.Do(func() {
		close(w.done)
		w.cancel()
	})
	w.wg.Wait()
}

func (w *reportWarmer) run() {
	ticker := time.NewTicker(w.every)
	defer ticker.Stop()
	var warmed warmMark
	for {
		if mark, ok := w.reports.warmMark(); ok && mark != warmed {
			err := w.reports.warm(w.ctx)
			switch {
			case err == nil:
				warmed = mark
			case errors.Is(err, context.Canceled):
				return
			default:
				// Tried again at the next check.
				slog.Warn("analytics: warm the past days", "error", err)
			}
		}
		select {
		case <-ticker.C:
		case <-w.done:
			return
		}
	}
}

// warmMark is what the days to warm depend on: the schedule loaded and today's
// service date. A new one of either is a reason to warm again.
type warmMark struct {
	source   string
	loadedAt int64
	today    string
}

func (r *serviceReporter) warmMark() (warmMark, bool) {
	ix := r.index()
	if ix == nil {
		return warmMark{}, false
	}
	load := loadOf(ix)
	return warmMark{source: load.source, loadedAt: load.loadedAt.UnixNano(), today: ix.ServiceDate(r.now())}, true
}

// warm works out, newest first and one at a time, the days the Analytics page
// opens on that the store does not keep yet, and keeps each. With retention
// on it starts at the first day the location pruner has deleted none of:
// an older day would be worked out from what is left of its reports, and its
// row deleted again at the next prune. A day that fails, to work out or to
// keep, does not stop the older ones; the pass returns the failures, so the
// warmer tries again at its next check.
func (r *serviceReporter) warm(ctx context.Context) error {
	ix := r.index()
	if ix == nil {
		return errNoSchedule
	}
	today, err := time.Parse(gtfsDateLayout, ix.ServiceDate(r.now()))
	if err != nil {
		return err
	}
	last := today.AddDate(0, 0, -2)
	first := today.AddDate(0, 0, -2*slices.Max(analyticsPeriods))
	if r.retention > 0 {
		if kept := r.firstRetainedDay(ix); kept.After(first) {
			first = kept
		}
	}
	if first.After(last) {
		return nil
	}
	stored, err := r.storedDays(ctx, first, last)
	if err != nil {
		return err
	}
	var failed []error
	for day := last; !day.Before(first); day = day.AddDate(0, 0, -1) {
		if _, ok := stored[day.Format(reportDateLayout)]; ok {
			continue
		}
		if err := r.warmDay(ctx, day); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed = append(failed, fmt.Errorf("%s: %w", day.Format(reportDateLayout), err))
		}
	}
	return errors.Join(failed...)
}

// warmDay works out day and keeps its totals, waiting until they are kept.
func (r *serviceReporter) warmDay(ctx context.Context, day time.Time) error {
	rep, read, err := r.reportRead(ctx, day.Format(reportDateLayout))
	if err != nil {
		return err
	}
	if read == nil {
		// From memory: worked out before it was old enough to keep, or its
		// save failed, so no read is keeping it.
		return r.saveDay(day, rep)
	}
	select {
	case <-read.saved:
		return read.saveErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// firstRetainedDay is the first service date that starts after the cutoff
// the location pruner deletes reports before, as of now: the first day all
// of whose reports are still kept.
func (r *serviceReporter) firstRetainedDay(ix *rider.Index) time.Time {
	cutoff := r.now().Add(-r.retention)
	y, m, d := cutoff.In(ix.Timezone()).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if start, err := rider.ServiceDayStart(day.Format(gtfsDateLayout), ix.Timezone()); err != nil || start.Before(cutoff) {
		day = day.AddDate(0, 0, 1)
	}
	return day
}
