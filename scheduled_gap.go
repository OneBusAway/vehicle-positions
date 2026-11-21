package main

import (
	"math"
	"slices"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// stopSchedule is the headway service at one stop of a route's direction:
// the frequencies.txt windows of each trip that serves it, shifted to when
// that trip's runs reach the stop.
type stopSchedule struct {
	trips [][]stopWindow
}

// stopWindow is a frequencies.txt row as it reaches a stop: a run every
// headway passing it from from up to but not including to.
type stopWindow struct {
	from, to time.Time
	headway  time.Duration
}

// timeSpan is from up to but not including to.
type timeSpan struct {
	from, to time.Time
}

// addTrip adds a trip's frequencies.txt rows on the service day starting at
// dayStart, its runs reaching the stop offset after leaving the first. A row
// with no headway is a single run, which sets no gap between vehicles.
func (s *stopSchedule) addTrip(rows []rider.Frequency, dayStart time.Time, offset time.Duration) {
	windows := make([]stopWindow, 0, len(rows))
	for _, row := range rows {
		if row.Headway <= 0 {
			continue
		}
		windows = append(windows, stopWindow{from: dayStart.Add(row.StartTime + offset), to: dayStart.Add(row.EndTime + offset), headway: row.Headway})
	}
	s.trips = append(s.trips, windows)
}

// stretches are the spans of scheduled service at the stop, in time order:
// every trip's windows, each held one headway past its end so a late last
// run still counts, joined where they meet or overlap. A gap is judged only
// between two passings in one stretch, so the evening's first run is not a
// gap after the morning's last.
func (s *stopSchedule) stretches() []timeSpan {
	var spans []timeSpan
	for _, windows := range s.trips {
		for _, w := range windows {
			spans = append(spans, timeSpan{w.from, w.to.Add(w.headway)})
		}
	}
	slices.SortFunc(spans, func(a, b timeSpan) int { return a.from.Compare(b.from) })
	out := make([]timeSpan, 0, len(spans))
	for _, span := range spans {
		if n := len(out); n > 0 && !span.from.After(out[n-1].to) {
			if span.to.After(out[n-1].to) {
				out[n-1].to = span.to
			}
			continue
		}
		out = append(out, span)
	}
	return out
}

// stretchAt is the index of the stretch that holds t, or -1.
func stretchAt(stretches []timeSpan, t time.Time) int {
	return slices.IndexFunc(stretches, func(span timeSpan) bool {
		return !t.Before(span.from) && t.Before(span.to)
	})
}

// gapAt is the scheduled gap at the stop at t, to the second: one over the
// sum of the rates of the trips due there then, so two patterns every 10
// minutes that share the stop make it every 5. A trip's rate is one run a
// headway in the window that holds t or, failing that, in the window that
// ended less than a headway before it, so a late last run is held to the gap
// it was due at. It is zero when no trip is due, which no stretch holds, and
// a second at the least.
func (s *stopSchedule) gapAt(t time.Time) time.Duration {
	rate := 0.0
	for _, windows := range s.trips {
		if w, ok := windowAt(windows, t); ok {
			rate += 1 / w.headway.Seconds()
		}
	}
	if rate == 0 {
		return 0
	}
	return max(time.Second, time.Duration(math.Round(1/rate))*time.Second)
}

// windowAt is the one of a trip's windows that holds t, or else the latest
// to end of those whose last headway does. frequencies.txt allows no overlap
// between a trip's windows, so at most one holds t, but a short window can
// end inside the last headway of the one before it.
func windowAt(windows []stopWindow, t time.Time) (stopWindow, bool) {
	for _, w := range windows {
		if !t.Before(w.from) && t.Before(w.to) {
			return w, true
		}
	}
	var last stopWindow
	found := false
	for _, w := range windows {
		if !t.Before(w.to) && t.Before(w.to.Add(w.headway)) && (!found || w.to.After(last.to)) {
			last, found = w, true
		}
	}
	return last, found
}
