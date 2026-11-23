package main

import (
	"math"
	"slices"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// frequentSpacing is the longest spacing between two departures in a row of
// a stop's timetable that makes frequent service there: every 12 minutes,
// five or more an hour, TfL's line for high-frequency routes. Riders turn up
// at random at a stop served that often, so the gap between vehicles is what
// they wait. Served less often, they time their trips by the timetable, and
// On time already says how well it was kept.
const frequentSpacing = 12 * time.Minute

// stopSchedule is the frequent service at one stop of a route's direction:
// the frequencies.txt windows of each trip that serves it, shifted to when
// that trip's runs reach the stop, and its timetable where it runs every
// frequentSpacing or more often.
type stopSchedule struct {
	trips [][]stopWindow
	// timetable is a window for each two departures in a row of the
	// timetable no more than frequentSpacing apart, and timetabled those
	// departures, both in time order.
	timetable  []stopWindow
	timetabled []time.Time
}

// stopWindow is a frequencies.txt row as it reaches a stop, or a frequent
// spacing of a timetable: a run every headway passing it from from up to but
// not including to.
type stopWindow struct {
	from, to time.Time
	headway  time.Duration
}

// timeSpan is from up to but not including to.
type timeSpan struct {
	from, to time.Time
}

// scheduleStops lays out the frequent service at each stop in schedules from
// a direction's trips on the service day starting at dayStart, keys being
// each trip's stopKeys. Every trip that serves a stop other than as its last,
// where nobody boards, counts there: a trip in frequencies.txt adds its
// windows, shifted to when its runs reach the stop, and a timetabled trip its
// departure to the stop's timetable. Analytics lays out the stops it measures
// gaps at this way, and the bunched alert the stop a vehicle is heading to,
// so the two agree on how often a stop is served.
func scheduleStops(schedules map[stopKey]*stopSchedule, trips []*rider.TripInfo, keys [][]stopKey, dayStart time.Time) {
	departures := make(map[stopKey][]time.Time)
	for j, trip := range trips {
		rows := trip.Frequencies()
		for i, key := range keys[j][:len(keys[j])-1] {
			s, ok := schedules[key]
			if !ok {
				continue
			}
			if trip.InFrequencies() {
				s.addTrip(rows, dayStart, trip.StopTimes[i].Departure-trip.StopTimes[0].Departure)
			} else {
				departures[key] = append(departures[key], dayStart.Add(trip.StopTimes[i].Departure))
			}
		}
	}
	for key, times := range departures {
		schedules[key].setTimetable(times)
	}
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

// setTimetable sets the stop's timetable: when the direction's timetabled
// trips are due to leave it, in any order. Two trips due at the same time
// are one departure, as a rider waiting there sees them. Where two
// departures in a row are no more than frequentSpacing apart, the timetable
// is frequent service, and their spacing is the scheduled gap of a vehicle
// passing nearer the later of them than any other departure: from halfway
// after the earlier up to halfway to the next. So a vehicle on time, or a
// little early or late, is judged against the gap riders waiting for it were
// promised, not the one after it. The first spacing of a run of them holds
// from its first departure, and the last up to its last departure, then a
// spacing more, as a frequencies.txt window is held past its end. Further
// apart, the timetable is not measured by gaps.
func (s *stopSchedule) setTimetable(departures []time.Time) {
	times := slices.SortedFunc(slices.Values(departures), time.Time.Compare)
	times = slices.CompactFunc(times, time.Time.Equal)
	// frequent reports whether the spacing up to the i'th departure is.
	frequent := func(i int) bool {
		return i > 0 && i < len(times) && times[i].Sub(times[i-1]) <= frequentSpacing
	}
	halfway := func(i int) time.Time { return times[i-1].Add(times[i].Sub(times[i-1]) / 2) }
	s.timetable = make([]stopWindow, 0, len(times))
	s.timetabled = make([]time.Time, 0, len(times))
	for i, at := range times {
		if frequent(i) || frequent(i+1) {
			s.timetabled = append(s.timetabled, at)
		}
		if !frequent(i) {
			continue
		}
		w := stopWindow{from: times[i-1], to: at, headway: at.Sub(times[i-1])}
		if frequent(i - 1) {
			w.from = halfway(i)
		}
		if frequent(i + 1) {
			w.to = halfway(i + 1)
		}
		s.timetable = append(s.timetable, w)
	}
}

// timetabledAt reports whether a timetabled trip due to leave the stop at at
// is part of the timetable's frequent service there. Its runs are then in
// the order of vehicles at the stop; otherwise On time alone judges them.
func (s *stopSchedule) timetabledAt(at time.Time) bool {
	_, found := slices.BinarySearchFunc(s.timetabled, at, time.Time.Compare)
	return found
}

// due are the sets of windows whose rates add at the stop: each trip's, and
// the timetable's.
func (s *stopSchedule) due() [][]stopWindow {
	return append(slices.Clip(s.trips), s.timetable)
}

// stretches are the spans of scheduled service at the stop, in time order:
// every window, each held one headway past its end so a late last run still
// counts, joined where they meet or overlap. A gap is judged only between two
// passings in one stretch, so the evening's first run is not a gap after the
// morning's last.
func (s *stopSchedule) stretches() []timeSpan {
	var spans []timeSpan
	for _, windows := range s.due() {
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
// sum of the rates of the trips and the timetable due there then, so two
// patterns every 10 minutes that share the stop make it every 5. A trip's
// rate is one run a headway in the window that holds t or, failing that, in
// the window that ended less than a headway before it, so a late last run is
// held to the gap it was due at. The timetable's is one run a spacing, the
// spacing up to its departure nearest t, as setTimetable lays them out.
// Either counts only until another trip or the timetable takes over from
// that window (takenOver). It is zero when nothing is due, which no stretch
// holds, and a second at the least.
func (s *stopSchedule) gapAt(t time.Time) time.Duration {
	due := s.due()
	rate := 0.0
	for i, windows := range due {
		if w, ok := windowAt(windows, t); ok && !takenOver(due, i, w, t) {
			rate += 1 / w.headway.Seconds()
		}
	}
	if rate == 0 {
		return 0
	}
	return max(time.Second, time.Duration(math.Round(1/rate))*time.Second)
}

// takenOver reports whether another set of due has taken over at t from w,
// the window of due[i] windowAt found for t: one due at t whose run began
// after the last headway of w's run did, so that at most one of w's runs is
// due after it began. w's run is its unbroken chain of windows (since and
// until), so a trip written as back-to-back rows and a frequent timetable,
// laid out a window a spacing, are measured to the end of their service,
// not of the window that holds t. A trip's window that ends as another
// trip's begins is then held no longer, just as a trip's own next row comes
// before the row that has just ended, so the same service gets the same gap
// whether a feed writes it as rows of one trip or as one trip a row. Each
// trip's windows are shifted by its own time to the stop, so where the next
// trip is quicker to it, its first run is due there before w ends, and w
// counts no more from that run on, through the next trip's last headway too.
// A set already running as w's run's last headway began, two patterns
// sharing the stop, does not take over. Nor does one that began no later
// than w's run did, when that run is shorter than a headway, so the set due
// at t that began last always counts.
func takenOver(due [][]stopWindow, i int, w stopWindow, t time.Time) bool {
	end := until(due[i], w)
	last := end.to.Add(-end.headway)
	if began := since(due[i], w); began.After(last) {
		last = began
	}
	for j, windows := range due {
		if j == i {
			continue
		}
		if v, ok := windowAt(windows, t); ok && since(windows, v).After(last) {
			return true
		}
	}
	return false
}

// since is when the run of windows that w is part of began: w's start, or
// that of the earliest window before it in an unbroken chain, each ending as
// the next begins, as a trip's rows and the timetable's spacings can. A
// window that ends as it begins is no link, or the walk back would not end.
func since(windows []stopWindow, w stopWindow) time.Time {
	from := w.from
	for {
		i := slices.IndexFunc(windows, func(v stopWindow) bool { return v.to.Equal(from) && v.from.Before(from) })
		if i < 0 {
			return from
		}
		from = windows[i].from
	}
}

// until is the last window of the run that w is part of: w, or the latest
// window after it in an unbroken chain, each beginning as the one before
// ends. A window that ends as it begins is no link, as in since.
func until(windows []stopWindow, w stopWindow) stopWindow {
	for {
		i := slices.IndexFunc(windows, func(v stopWindow) bool { return v.from.Equal(w.to) && v.to.After(w.to) })
		if i < 0 {
			return w
		}
		w = windows[i]
	}
}

// holding is the one of windows that holds t, if any.
func holding(windows []stopWindow, t time.Time) (stopWindow, bool) {
	i := slices.IndexFunc(windows, func(w stopWindow) bool {
		return !t.Before(w.from) && t.Before(w.to)
	})
	if i < 0 {
		return stopWindow{}, false
	}
	return windows[i], true
}

// windowAt is the one of a set's windows that holds t, or else the latest to
// end of those whose last headway does. A set's windows never overlap:
// frequencies.txt allows none between a trip's, and a timetable's follow one
// another. So at most one holds t, but a short window can end inside the
// last headway of the one before it.
func windowAt(windows []stopWindow, t time.Time) (stopWindow, bool) {
	if w, ok := holding(windows, t); ok {
		return w, true
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
