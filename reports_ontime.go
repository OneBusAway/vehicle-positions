package main

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// reportFixWindow is how close in time a fix must be, on each side, to the
// moment a vehicle passed a stop for that moment to count as known. Two
// minutes is two report intervals of slack for either driver app, and short
// enough that a vehicle cannot have stood at the stop for long unseen. A stop
// with a wider gap around it is unknown, not late: the phone lost signal or
// the app stopped, and the report cannot tell how the vehicle ran there.
const reportFixWindow = 2 * time.Minute

// stopVerdict is how a run passed one stop.
type stopVerdict int

const (
	stopUnknown stopVerdict = iota
	stopOnTime
	stopEarly
	stopLate
)

// placedFix is a location point placed on its trip's shape.
type placedFix struct {
	at    time.Time
	along float64
}

// runEvaluation is how one run of a scheduled trip kept to the schedule.
type runEvaluation struct {
	// departure is how far from schedule the run left its first stop, and
	// arrival how far from schedule it reached its last; positive is late.
	// Each is nil when unknown.
	departure *time.Duration
	arrival   *time.Duration
	counts    stopCounts
}

// stopCounts tallies the verdicts at the stops a run is judged at.
type stopCounts struct {
	onTime, early, late, unknown int
}

func (c *stopCounts) add(v stopVerdict) {
	switch v {
	case stopOnTime:
		c.onTime++
	case stopEarly:
		c.early++
	case stopLate:
		c.late++
	default:
		c.unknown++
	}
}

func (c *stopCounts) merge(o stopCounts) {
	c.onTime += o.onTime
	c.early += o.early
	c.late += o.late
	c.unknown += o.unknown
}

// onTimePercent is the share of the stops with a known passing time that were
// passed on time, or nil when none is known.
func (c stopCounts) onTimePercent() *float64 {
	return percent(c.onTime, c.onTime+c.early+c.late)
}

// unknownPercent is the share of the judged stops with no known passing
// time, or nil when no stop was judged.
func (c stopCounts) unknownPercent() *float64 {
	return percent(c.unknown, c.onTime+c.early+c.late+c.unknown)
}

// percent is part of whole as a percentage to one decimal place, or nil when
// whole is zero.
func percent(part, whole int) *float64 {
	if whole == 0 {
		return nil
	}
	p := math.Round(1000*float64(part)/float64(whole)) / 10
	return &p
}

// verdict judges a deviation from schedule, positive late, against the
// on-time window. Both ends of the window are on time.
func (s reportSettings) verdict(deviation time.Duration) stopVerdict {
	switch {
	case deviation < -s.onTimeEarly:
		return stopEarly
	case deviation > s.onTimeLate:
		return stopLate
	}
	return stopOnTime
}

// evaluateRuns judges trip on the service day starting at dayStart from the
// trails of the driver trips that ran it, one trail a run. A run leaves the first stop when it is more than
// stoppedAtDistance past it, the distance at which the feed stops calling it
// STOPPED_AT the stop, so a driver waiting at the first stop before departure
// is not counted early; it reaches the last stop when it comes within that
// distance of it. Every other stop is judged on departure too, the moment a
// timetable's times are kept to.
//
// The stops judged are the trip's timepoints, or every stop when the feed
// marks none of them. A trip whose shape was drawn through its stops is not
// placed at all, as tripLocator does not place one, so every stop is unknown.
//
// A trip run more than once is judged at each stop on the first run known to
// have passed it. Each run is placed and followed on its own: two vehicles'
// points put together read as one vehicle jumping between them, and a time
// worked out across the jump is one that neither vehicle kept.
func evaluateRuns(trip *rider.TripInfo, dayStart time.Time, trails [][]LocationPoint, s reportSettings) runEvaluation {
	var runs [][]placedFix
	if !trip.ShapeFromStops {
		for _, points := range trails {
			runs = append(runs, placeFixes(trip, points, s.maxShapeDistance))
		}
	}

	last := len(trip.StopTimes) - 1
	departure := func(i int) (time.Duration, bool) {
		stop := trip.StopTimes[i]
		at, ok := firstPassedAt(runs, math.Min(stop.AlongShape+stoppedAtDistance, trip.Shape.Length))
		return at.Sub(dayStart.Add(stop.Departure)), ok
	}
	arrival := func(i int) (time.Duration, bool) {
		stop := trip.StopTimes[i]
		at, ok := firstPassedAt(runs, math.Max(stop.AlongShape-stoppedAtDistance, 0))
		return at.Sub(dayStart.Add(stop.Arrival)), ok
	}

	var eval runEvaluation
	if d, ok := departure(0); ok {
		eval.departure = &d
	}
	if d, ok := arrival(last); ok {
		eval.arrival = &d
	}
	for _, i := range judgedStops(trip.StopTimes) {
		judge := departure
		if i == last {
			judge = arrival
		}
		verdict := stopUnknown
		if d, ok := judge(i); ok {
			verdict = s.verdict(d)
		}
		eval.counts.add(verdict)
	}
	return eval
}

// judgedStops returns the indexes of the stops a run is judged at: the
// timepoints, or all of them when the feed marks none.
func judgedStops(stops []rider.StopTimeInfo) []int {
	judged := make([]int, 0, len(stops))
	for i, stop := range stops {
		if stop.Timepoint {
			judged = append(judged, i)
		}
	}
	if len(judged) > 0 {
		return judged
	}
	for i := range stops {
		judged = append(judged, i)
	}
	return judged
}

// placeFixes places a trip's points on its shape as tripLocator places a
// driver vehicle's reports: in time order, each projected from where the one
// before it was placed, the first from the start of the shape, and a point
// further from the shape than the off-route distance plus its own accuracy
// left out with the hint carried past it. The hint keeps the two legs of an
// out-and-back, and the two passes of a loop, apart.
func placeFixes(trip *rider.TripInfo, points []LocationPoint, maxShapeDistance float64) []placedFix {
	sorted := slices.Clone(points)
	slices.SortStableFunc(sorted, func(a, b LocationPoint) int { return cmp.Compare(a.Timestamp, b.Timestamp) })

	fixes := make([]placedFix, 0, len(sorted))
	hint := 0.0
	for _, p := range sorted {
		proj := trip.Shape.Project(rider.LatLon{Lat: p.Latitude, Lon: p.Longitude}, &hint)
		tolerance := maxShapeDistance
		if p.Accuracy != nil {
			tolerance += math.Max(*p.Accuracy, 0)
		}
		if proj.DistanceToShape > tolerance {
			continue
		}
		fixes = append(fixes, placedFix{at: time.Unix(p.Timestamp, 0), along: proj.AlongShape})
		hint = proj.AlongShape
	}
	return fixes
}

// firstPassedAt is when the first of a trip's runs got target metres along
// the shape, unknown when no run's passing time is known.
func firstPassedAt(runs [][]placedFix, target float64) (time.Time, bool) {
	var first time.Time
	known := false
	for _, fixes := range runs {
		if at, ok := passedAt(fixes, target); ok && (!known || at.Before(first)) {
			first, known = at, true
		}
	}
	return first, known
}

// passedAt is when the vehicle first got target metres along the shape,
// interpolated between the last fix short of it and the first fix at or past
// it, to the second. It is unknown when no fix got that far, when the first
// fix was already there, and when either fix is more than reportFixWindow
// from the moment.
func passedAt(fixes []placedFix, target float64) (time.Time, bool) {
	for j, fix := range fixes {
		if fix.along < target {
			continue
		}
		if j == 0 {
			return time.Time{}, false
		}
		before := fixes[j-1]
		fraction := (target - before.along) / (fix.along - before.along)
		at := before.at.Add(time.Duration(fraction * float64(fix.at.Sub(before.at)))).Round(time.Second)
		if at.Sub(before.at) > reportFixWindow || fix.at.Sub(at) > reportFixWindow {
			return time.Time{}, false
		}
		return at, true
	}
	return time.Time{}, false
}
