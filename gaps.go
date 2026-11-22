package main

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const (
	// bunchedBelow and longGapAbove are the bands a gap between vehicles is
	// judged by, as shares of the scheduled gap: bunched under 25% (two
	// vehicles under 2.5 minutes apart on a 10-minute route), the NYC
	// Comptroller's rule, also credited to NYCT and SFMTA; long over 150%, the
	// TTC's and Swiftly's. A share of the scheduled gap holds from a 3-minute
	// route to a 30-minute one, where a fixed number of minutes would not.
	bunchedBelow = 0.25
	longGapAbove = 1.5
	// gapRatioTop is the last tenth of the scheduled gap the kept totals count
	// gaps by: 300% and over.
	gapRatioTop = 30
	// passingLateGrace is how much longer than its trip's scheduled run time
	// a run may take after its last fix to reach a stop its trail never got
	// to. A run still running, or ended, later than that is taken to have
	// been left running by a driver who forgot to end it, and holds no place
	// in the order at that stop after then. It is the 90 minutes the rest of
	// the server lets a run be late: RIDER_SCHEDULE_LATE's default in rider
	// mode, and how long after the day before's frequencies.txt window a run
	// may leave and still be that day's (rider's runningLateGrace).
	passingLateGrace = 90 * time.Minute
)

// directionGaps is one direction of a route's frequent service on the day,
// from frequencies.txt or a timetable: the gaps between consecutive vehicles
// judged at its stops.
type directionGaps struct {
	// DirectionID is nil when the feed gives the trips none; Headsign then
	// tells the directions apart.
	DirectionID *int   `json:"direction_id"`
	Headsign    string `json:"headsign"`
	GapsJudged  int    `json:"gaps_judged"`
	Bunched     int    `json:"bunched"`
	LongGaps    int    `json:"long_gaps"`
	Unknown     int    `json:"unknown"`
	// ExtraWaitS is nil when no gap was judged.
	ExtraWaitS *int `json:"extra_wait_s"`
	// ScheduledGapS is the scheduled gap of the most gaps judged, the
	// shortest of those as common; nil when no gap was judged.
	ScheduledGapS *int `json:"scheduled_gap_s"`

	// hours are the gaps by the service day's hour the later vehicle passed
	// in, and ratios the gaps judged by tenths of their scheduled gap, as the
	// kept totals keep them.
	hours  []gapHourTotals
	ratios [gapRatioTop + 1]int
	// trips are the GTFS trips of the direction, and runs the runs measured
	// in it and every other run of its trips in frequencies.txt, in the order
	// they left the first stop each is measured at, the earlier id first of
	// two at once. A run whose passing there is not known is in the order by
	// the earliest it could have left, and one that ended before leaving it,
	// or that is measured at no stop, by when it started.
	trips []string
	runs  []runGaps
}

// runGaps is one run of a direction's frequent service as its route's day
// lists it: when it left the first stop it is measured at, the gap there to
// the run before it, and how many of the gaps it closed, one at each stop it
// is measured at, were judged and how many of those bunched.
type runGaps struct {
	id int64
	// left is when the run left that stop, zero when that is not known or
	// when it ended before leaving, which served false tells apart. A run
	// measured at no stop has no left, and served false when it ended before
	// leaving its trip's first stop.
	left   time.Time
	served bool
	// gap is nil when the run closed none there: its passing is not known,
	// or it was the first of a stretch of service.
	gap             *stopGap
	judged, bunched int
}

// gapCounts are gaps between vehicles: those judged, those of them bunched
// and long, and those unknown, with the sums extra wait is worked out from.
type gapCounts struct {
	Gaps     int `json:"gaps"`
	Bunched  int `json:"bunched"`
	LongGaps int `json:"long_gaps"`
	Unknown  int `json:"unknown"`
	// SumGapS and SumGapSq are the sum of the gaps judged in seconds and the
	// sum of their squares; SumScheduledS and SumScheduledSq the same of
	// their scheduled gaps.
	SumGapS        int64 `json:"sum_gap_s"`
	SumGapSq       int64 `json:"sum_gap_sq"`
	SumScheduledS  int64 `json:"sum_scheduled_s"`
	SumScheduledSq int64 `json:"sum_scheduled_sq"`
}

func (c *gapCounts) add(g stopGap) {
	if g.unknown {
		c.Unknown++
		return
	}
	h, scheduled := int64(g.gap/time.Second), int64(g.scheduled/time.Second)
	c.Gaps++
	switch {
	case g.bunched():
		c.Bunched++
	case g.long():
		c.LongGaps++
	}
	c.SumGapS += h
	c.SumGapSq += h * h
	c.SumScheduledS += scheduled
	c.SumScheduledSq += scheduled * scheduled
}

// isBunched reports whether a gap of h seconds between two vehicles is
// bunched against a scheduled gap of scheduled seconds. The live map's
// bunched alert judges by it too, so it and Analytics call the same gaps
// bunched.
func isBunched(h, scheduled int64) bool {
	return float64(h) < bunchedBelow*float64(scheduled)
}

// bunched reports whether a gap judged is bunched, by isBunched to the
// second.
func (g stopGap) bunched() bool {
	return isBunched(int64(g.gap/time.Second), int64(g.scheduled/time.Second))
}

// long reports whether a gap judged is over longGapAbove of its scheduled
// gap, to the second.
func (g stopGap) long() bool {
	return float64(g.gap/time.Second) > longGapAbove*float64(g.scheduled/time.Second)
}

// extraWait is Excess Wait Time as TfL defines it, to the second: the
// average wait of a rider who turned up at random, sum(h^2) / 2 sum(h) over
// the gaps judged, less the same over their scheduled gaps. It is below zero
// when the vehicles came more often than scheduled, and nil when no gap was
// judged.
func (c gapCounts) extraWait() *int {
	if c.Gaps == 0 {
		return nil
	}
	s := int(math.Round(randomWait(c.SumGapS, c.SumGapSq) - randomWait(c.SumScheduledS, c.SumScheduledSq)))
	return &s
}

// randomWait is the average wait for a vehicle, in seconds, of a rider who
// turned up at random, over gaps adding up to sum with squares adding up to
// squares. Gaps of nothing, vehicles passing in the same second, keep nobody
// waiting.
func randomWait(sum, squares int64) float64 {
	if sum == 0 {
		return 0
	}
	return float64(squares) / float64(2*sum)
}

// gapRatioTenth is which tenth of its scheduled gap a judged gap is in, the
// last for 300% and over.
func gapRatioTenth(g stopGap) int {
	return min(gapRatioTop, int(10*g.gap/g.scheduled))
}

// mostOften is the scheduled gap seen most often in seconds, the shortest of
// those seen as often, or nil when none was seen.
func mostOften(seen map[time.Duration]int) *int {
	best, bestCount := time.Duration(0), 0
	for _, gap := range slices.Sorted(maps.Keys(seen)) {
		if seen[gap] > bestCount {
			best, bestCount = gap, seen[gap]
		}
	}
	if bestCount == 0 {
		return nil
	}
	s := int(best / time.Second)
	return &s
}

// directionKey groups a route's trips by direction: its direction_id, or
// when the feed gives none its headsign, which then nearly always names the
// destination.
type directionKey struct {
	id       int
	headsign string
}

func directionOf(trip *rider.TripInfo) directionKey {
	if trip.DirectionID >= 0 {
		return directionKey{id: trip.DirectionID}
	}
	return directionKey{id: -1, headsign: trip.Headsign}
}

// routeGaps is the gaps between vehicles on a route's frequent service on
// the service day starting at dayStart, by direction_id then headsign: one
// entry for each direction with a trip in frequencies.txt, or with a
// timetable every frequentSpacing or more often at a stop it is measured at,
// and none for a route with neither. trips are the route's trips that day in
// order of departure, runs the driver trips that ran each and trails the
// trail of each run, as buildServiceReport takes them.
func routeGaps(rep *serviceReport, trips []*rider.TripInfo, dayStart time.Time, runs map[string][]TripSummary, trails map[string][][]LocationPoint, s reportSettings) []directionGaps {
	groups := make(map[directionKey][]*rider.TripInfo)
	for _, trip := range trips {
		key := directionOf(trip)
		groups[key] = append(groups[key], trip)
	}
	keys := slices.SortedFunc(maps.Keys(groups), func(a, b directionKey) int {
		return cmp.Or(cmp.Compare(a.id, b.id), cmp.Compare(a.headsign, b.headsign))
	})
	out := make([]directionGaps, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		stops := directionStops(group, dayStart, runs, trails, s.maxShapeDistance)
		if !slices.ContainsFunc(group, (*rider.TripInfo).InFrequencies) && !frequentTimetable(stops) {
			continue
		}
		d := directionGaps{Headsign: group[0].Headsign}
		if key.id >= 0 {
			id := key.id
			d.DirectionID = &id
		}
		var total gapCounts
		hours := make(map[int]*gapHourTotals)
		scheduled := make(map[time.Duration]int)
		listed := make(map[int64]*runGaps)
		// first is the stop each run is measured at first, and order when it
		// left there, as the runs are listed.
		first := make(map[int64]stopKey)
		order := make(map[int64]time.Time)
		for key, stop := range stops {
			for _, l := range stop.leaving {
				listed[l.run.ID] = &runGaps{id: l.run.ID, left: l.passing.at, served: l.served}
				first[l.run.ID] = key
				order[l.run.ID] = l.order()
			}
		}
		// A trip in frequencies.txt none of whose stops is measured, as when
		// its only timepoint is its last stop, is in no stop's order. Its runs
		// are listed all the same, by when they started, and one that ended
		// before leaving the trip's first stop as not served.
		for _, trip := range group {
			if !trip.InFrequencies() {
				continue
			}
			for k, run := range runs[trip.ID] {
				if _, ok := listed[run.ID]; !ok {
					listed[run.ID] = &runGaps{id: run.ID, served: !endedBeforeLeaving(trip, run, trails[trip.ID][k], s.maxShapeDistance)}
					order[run.ID] = run.StartTime
				}
			}
		}
		for key, stop := range stops {
			for _, g := range stop.gaps() {
				hour := serviceHour(rep, g.at)
				h, ok := hours[hour]
				if !ok {
					h = &gapHourTotals{Hour: hour}
					hours[hour] = h
				}
				h.add(g)
				total.add(g)
				run := listed[g.run]
				if !g.unknown {
					d.ratios[gapRatioTenth(g)]++
					scheduled[g.scheduled]++
					run.judged++
					if g.bunched() {
						run.bunched++
					}
				}
				if first[g.run] == key {
					run.gap = &g
				}
			}
		}
		d.trips = make([]string, 0, len(group))
		for _, trip := range group {
			d.trips = append(d.trips, trip.ID)
		}
		d.runs = make([]runGaps, 0, len(listed))
		for _, run := range listed {
			d.runs = append(d.runs, *run)
		}
		slices.SortFunc(d.runs, func(a, b runGaps) int {
			return cmp.Or(order[a.id].Compare(order[b.id]), cmp.Compare(a.id, b.id))
		})
		d.GapsJudged, d.Bunched, d.LongGaps, d.Unknown = total.Gaps, total.Bunched, total.LongGaps, total.Unknown
		d.ExtraWaitS = total.extraWait()
		d.ScheduledGapS = mostOften(scheduled)
		d.hours = make([]gapHourTotals, 0, len(hours))
		for _, h := range hours {
			d.hours = append(d.hours, *h)
		}
		slices.SortFunc(d.hours, func(a, b gapHourTotals) int { return cmp.Compare(a.Hour, b.Hour) })
		out = append(out, d)
	}
	return out
}

// stopKey names a stop of a trip by its stop_id and which visit to it it is,
// so a loop's first stop and its last, the same stop again, are apart.
type stopKey struct {
	stopID string
	visit  int
}

func stopKeys(stops []rider.StopTimeInfo) []stopKey {
	visits := make(map[string]int, len(stops))
	keys := make([]stopKey, 0, len(stops))
	for _, stop := range stops {
		keys = append(keys, stopKey{stop.StopID, visits[stop.StopID]})
		visits[stop.StopID]++
	}
	return keys
}

// stopService is one measured stop of a direction: the schedule of the trips
// that serve it and how each of their runs passed it. leaving are the runs
// measured at it first, served or not.
type stopService struct {
	schedule stopSchedule
	passings []stopPassing
	leaving  []runLeaving
}

// runLeaving is a run at the first stop it is measured at: how it passed
// there, and served false when it ended before leaving.
type runLeaving struct {
	run     TripSummary
	passing stopPassing
	served  bool
}

// order is when the run left the stop, the earliest it could have when that
// is not known, or when it started when it did not leave.
func (l runLeaving) order() time.Time {
	switch {
	case !l.served:
		return l.run.StartTime
	case l.passing.at.IsZero():
		return l.passing.from
	}
	return l.passing.at
}

// directionStops gathers a direction's trips at the stops gaps are measured
// at: those On time judges on any of them (the timepoints, or every stop when
// the feed marks none) except a trip's last, where nobody boards. Each stop's
// scheduled gap comes from scheduleStops. A trip in frequencies.txt adds its
// runs to the order. A timetabled trip adds its runs to the order only when
// its departure is part of the timetable's frequent service, so the order
// holds the vehicles the scheduled gap counts. runs and trails are by GTFS
// trip id, a trail for each run; a trip whose runs are in no stop's order is
// not placed.
func directionStops(trips []*rider.TripInfo, dayStart time.Time, runs map[string][]TripSummary, trails map[string][][]LocationPoint, maxShapeDistance float64) map[stopKey]*stopService {
	stops := make(map[stopKey]*stopService)
	tripKeys := make([][]stopKey, len(trips))
	for j, trip := range trips {
		keys := stopKeys(trip.StopTimes)
		tripKeys[j] = keys
		for _, i := range judgedStops(trip.StopTimes) {
			if i < len(keys)-1 {
				stops[keys[i]] = &stopService{}
			}
		}
	}
	schedules := make(map[stopKey]*stopSchedule, len(stops))
	for key, stop := range stops {
		schedules[key] = &stop.schedule
	}
	scheduleStops(schedules, trips, tripKeys, dayStart)
	for j, trip := range trips {
		keys := tripKeys[j]
		// inOrder are the stops at which the trip's runs are in the order.
		inOrder := make([]int, 0, len(keys))
		for i, key := range keys[:len(keys)-1] {
			if stop, ok := stops[key]; ok && (trip.InFrequencies() || stop.schedule.timetabledAt(dayStart.Add(trip.StopTimes[i].Departure))) {
				inOrder = append(inOrder, i)
			}
		}
		if len(inOrder) == 0 {
			continue
		}
		tripRuns := runs[trip.ID]
		// A trip whose shape was drawn through its stops is not placed, as
		// evaluateRuns does not place it, so its runs pass every stop unknown.
		fixes := make([][]placedFix, len(tripRuns))
		if !trip.ShapeFromStops {
			for k := range tripRuns {
				fixes[k] = placeFixes(trip, trails[trip.ID][k], maxShapeDistance)
			}
		}
		open := arrives(trip) - departs(trip) + passingLateGrace
		for _, i := range inOrder {
			stop := stops[keys[i]]
			for k, run := range tripRuns {
				p, served := runPassing(fixes[k], leftStopAt(trip, i), run, open)
				p.run = run.ID
				if served {
					stop.passings = append(stop.passings, p)
				}
				if i == inOrder[0] {
					stop.leaving = append(stop.leaving, runLeaving{run: run, passing: p, served: served})
				}
			}
		}
	}
	return stops
}

// endedBeforeLeaving reports whether run, of trip, ended before leaving the
// trip's first stop, as runPassing tells a run that did not serve a stop: its
// trail ends short of leaving it, and it ended within reportFixWindow of its
// last fix. A trip whose shape was drawn through its stops is not placed, as
// directionStops does not place it, so none of its runs is known to have.
func endedBeforeLeaving(trip *rider.TripInfo, run TripSummary, trail []LocationPoint, maxShapeDistance float64) bool {
	if trip.ShapeFromStops {
		return false
	}
	// open only bounds when a run that served the stop could have left it,
	// so it does not change whether the run served it.
	_, served := runPassing(placeFixes(trip, trail, maxShapeDistance), leftStopAt(trip, 0), run, 0)
	return !served
}

// frequentTimetable reports whether any of a direction's stops has a
// timetable every frequentSpacing or more often.
func frequentTimetable(stops map[stopKey]*stopService) bool {
	for _, stop := range stops {
		if len(stop.schedule.timetable) > 0 {
			return true
		}
	}
	return false
}

// stopPassing is when the run with id run left a stop: at when known, or
// else some time from from up to to.
type stopPassing struct {
	at, from, to time.Time
	run          int64
}

// between reports whether a passing not known could fall between a and b.
func (p stopPassing) between(a, b time.Time) bool {
	return p.from.Before(b) && p.to.After(a)
}

// runPassing is when run got target metres along its trip's shape, from its
// fixes placed on the shape. It is known when passedAt knows it, as On time
// works out a departure. Otherwise the run got there between its last fix
// short of target and its first fix past it, the trip's start standing in for
// a fix before when the trail starts past target. When the trail ends short
// of target, the run got there after its last fix, or its start when it sent
// none, and before the trip's end, but no more than open after it: by then a
// run on its way would have got there, so a trip left running, or ended hours
// later, does not hold the stop's gaps unknown for the rest of the day.
// served is false when the trail ends short of target and the trip ended
// within reportFixWindow of its last fix: it ended early or turned short, and
// did not serve the stop.
func runPassing(fixes []placedFix, target float64, run TripSummary, open time.Duration) (p stopPassing, served bool) {
	if at, ok := passedAt(fixes, target); ok {
		return stopPassing{at: at}, true
	}
	p.from = run.StartTime
	if j := slices.IndexFunc(fixes, func(f placedFix) bool { return f.along >= target }); j >= 0 {
		if j > 0 {
			p.from = fixes[j-1].at
		}
		p.to = fixes[j].at
		return p, true
	}
	if len(fixes) > 0 {
		p.from = fixes[len(fixes)-1].at
	}
	if run.EndTime != nil && len(fixes) > 0 && run.EndTime.Sub(p.from) <= reportFixWindow {
		return stopPassing{}, false
	}
	p.to = p.from.Add(open)
	if run.EndTime != nil && run.EndTime.Before(p.to) {
		p.to = *run.EndTime
	}
	return p, true
}

// stopGap is the gap before a passing at a stop: from the passing before it,
// the two in one stretch of service, and the scheduled gap then. It is
// unknown when a run whose passing is not known could have passed between
// them: a gap worked out across it could be twice the true one.
type stopGap struct {
	// at is the later passing, when a rider waiting there boarded.
	at             time.Time
	gap, scheduled time.Duration
	unknown        bool
	// run made the later passing and ahead the earlier.
	run, ahead int64
}

// gaps are the gaps between the known passings at the stop, ordered by time
// whichever runs made them, so a vehicle that overtakes another is gapped
// where it passed, and the earlier id first of two in the same second. The
// first passing of a stretch has no gap.
func (s *stopService) gaps() []stopGap {
	known := make([]stopPassing, 0, len(s.passings))
	var unknown []stopPassing
	for _, p := range s.passings {
		if p.at.IsZero() {
			unknown = append(unknown, p)
		} else {
			known = append(known, p)
		}
	}
	slices.SortFunc(known, func(a, b stopPassing) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.run, b.run)) })
	stretches := s.schedule.stretches()
	out := make([]stopGap, 0, len(known))
	for i := 1; i < len(known); i++ {
		a, b := known[i-1].at, known[i].at
		if n := stretchAt(stretches, b); n < 0 || n != stretchAt(stretches, a) {
			continue
		}
		g := stopGap{at: b, run: known[i].run, ahead: known[i-1].run}
		if slices.ContainsFunc(unknown, func(p stopPassing) bool { return p.between(a, b) }) {
			g.unknown = true
		} else {
			g.gap, g.scheduled = b.Sub(a), s.schedule.gapAt(b)
		}
		out = append(out, g)
	}
	return out
}
