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
)

// directionGaps is one direction of a route's frequencies.txt service on the
// day: the gaps between consecutive vehicles judged at its stops.
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
	case float64(h) < bunchedBelow*float64(scheduled):
		c.Bunched++
	case float64(h) > longGapAbove*float64(scheduled):
		c.LongGaps++
	}
	c.SumGapS += h
	c.SumGapSq += h * h
	c.SumScheduledS += scheduled
	c.SumScheduledSq += scheduled * scheduled
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

// routeGaps is the gaps between vehicles on a route's frequencies.txt trips
// on the service day starting at dayStart, one entry for each direction they
// run in, by direction_id then headsign, and none for a route run only by
// timetable. trips are the route's trips that day in order of departure,
// runs the driver trips that ran each and trails the trail of each run, as
// buildServiceReport takes them.
func routeGaps(rep *serviceReport, trips []*rider.TripInfo, dayStart time.Time, runs map[string][]TripSummary, trails map[string][][]LocationPoint, s reportSettings) []directionGaps {
	groups := make(map[directionKey][]*rider.TripInfo)
	for _, trip := range trips {
		if trip.InFrequencies() {
			key := directionOf(trip)
			groups[key] = append(groups[key], trip)
		}
	}
	keys := slices.SortedFunc(maps.Keys(groups), func(a, b directionKey) int {
		return cmp.Or(cmp.Compare(a.id, b.id), cmp.Compare(a.headsign, b.headsign))
	})
	out := make([]directionGaps, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		d := directionGaps{Headsign: group[0].Headsign}
		if key.id >= 0 {
			id := key.id
			d.DirectionID = &id
		}
		var total gapCounts
		hours := make(map[int]*gapHourTotals)
		scheduled := make(map[time.Duration]int)
		for _, stop := range directionStops(group, dayStart, runs, trails, s.maxShapeDistance) {
			for _, g := range stop.gaps() {
				hour := serviceHour(rep, g.at)
				h, ok := hours[hour]
				if !ok {
					h = &gapHourTotals{Hour: hour}
					hours[hour] = h
				}
				h.add(g)
				total.add(g)
				if !g.unknown {
					d.ratios[gapRatioTenth(g)]++
					scheduled[g.scheduled]++
				}
			}
		}
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
// that serve it and how each of their runs passed it.
type stopService struct {
	schedule stopSchedule
	passings []stopPassing
}

// directionStops gathers a direction's trips at the stops gaps are measured
// at: those On time judges on any of them (the timepoints, or every stop when
// the feed marks none) except a trip's last, where nobody boards. Every trip
// that serves such a stop other than as its last counts there, its windows
// towards the scheduled gap and its runs in the order. runs and trails are by
// GTFS trip id, a trail for each run.
func directionStops(trips []*rider.TripInfo, dayStart time.Time, runs map[string][]TripSummary, trails map[string][][]LocationPoint, maxShapeDistance float64) map[stopKey]*stopService {
	stops := make(map[stopKey]*stopService)
	for _, trip := range trips {
		keys := stopKeys(trip.StopTimes)
		for _, i := range judgedStops(trip.StopTimes) {
			if i < len(keys)-1 {
				stops[keys[i]] = &stopService{}
			}
		}
	}
	for _, trip := range trips {
		tripRuns := runs[trip.ID]
		// A trip whose shape was drawn through its stops is not placed, as
		// evaluateRuns does not place it, so its runs pass every stop unknown.
		fixes := make([][]placedFix, len(tripRuns))
		if !trip.ShapeFromStops {
			for k := range tripRuns {
				fixes[k] = placeFixes(trip, trails[trip.ID][k], maxShapeDistance)
			}
		}
		rows := trip.Frequencies()
		keys := stopKeys(trip.StopTimes)
		for i, key := range keys[:len(keys)-1] {
			stop, ok := stops[key]
			if !ok {
				continue
			}
			stop.schedule.addTrip(rows, dayStart, trip.StopTimes[i].Departure-trip.StopTimes[0].Departure)
			for k, run := range tripRuns {
				if p, served := runPassing(fixes[k], leftStopAt(trip, i), run); served {
					stop.passings = append(stop.passings, p)
				}
			}
		}
	}
	return stops
}

// stopPassing is when a run left a stop: at when known, or else some time
// from from up to to, with no end for a run still under way.
type stopPassing struct {
	at, from, to time.Time
}

// between reports whether a passing not known could fall between a and b.
func (p stopPassing) between(a, b time.Time) bool {
	return p.from.Before(b) && (p.to.IsZero() || p.to.After(a))
}

// runPassing is when run got target metres along its trip's shape, from its
// fixes placed on the shape. It is known when passedAt knows it, as On time
// works out a departure. Otherwise the run got there between its last fix
// short of target and its first fix past it, the trip's start standing in for
// a fix before when the trail starts past target, and the trip's end, or no
// end for a trip still running, for one after when the trail ends short of
// it. served is false when the trail ends short of target and the trip ended
// within reportFixWindow of its last fix: it ended early or turned short, and
// did not serve the stop.
func runPassing(fixes []placedFix, target float64, run TripSummary) (p stopPassing, served bool) {
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
	if run.EndTime == nil {
		return p, true
	}
	if len(fixes) > 0 && run.EndTime.Sub(p.from) <= reportFixWindow {
		return stopPassing{}, false
	}
	p.to = *run.EndTime
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
}

// gaps are the gaps between the known passings at the stop, ordered by time
// whichever runs made them, so a vehicle that overtakes another is gapped
// where it passed. The first passing of a stretch has no gap.
func (s *stopService) gaps() []stopGap {
	known := make([]time.Time, 0, len(s.passings))
	var unknown []stopPassing
	for _, p := range s.passings {
		if p.at.IsZero() {
			unknown = append(unknown, p)
		} else {
			known = append(known, p.at)
		}
	}
	slices.SortFunc(known, time.Time.Compare)
	stretches := s.schedule.stretches()
	out := make([]stopGap, 0, len(known))
	for i := 1; i < len(known); i++ {
		a, b := known[i-1], known[i]
		if n := stretchAt(stretches, b); n < 0 || n != stretchAt(stretches, a) {
			continue
		}
		g := stopGap{at: b}
		if slices.ContainsFunc(unknown, func(p stopPassing) bool { return p.between(a, b) }) {
			g.unknown = true
		} else {
			g.gap, g.scheduled = b.Sub(a), s.schedule.gapAt(b)
		}
		out = append(out, g)
	}
	return out
}
