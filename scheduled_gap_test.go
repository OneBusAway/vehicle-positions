package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// every is a frequencies.txt row: a run every headway from start to end.
func every(headway, start, end time.Duration) rider.Frequency {
	return rider.Frequency{StartTime: start, EndTime: end, Headway: headway}
}

func TestStopSchedule_GapAt(t *testing.T) {
	at := func(h, m, s int) time.Time { return reportDayStart.Add(hms(h, m, s)) }
	type trip struct {
		rows   []rider.Frequency
		offset time.Duration
	}
	tests := []struct {
		name  string
		trips []trip
		at    time.Time
		want  time.Duration
	}{
		{"one trip, the row that holds the time",
			[]trip{{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(15*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0}},
			at(7, 59, 59), 10 * time.Minute},
		{"the next row from its start",
			[]trip{{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(15*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0}},
			at(8, 0, 0), 15 * time.Minute},
		{"a stop later in the pattern: the row of when the run would have started",
			[]trip{{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(15*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 5 * time.Minute}},
			at(8, 4, 59), 10 * time.Minute},
		{"a late last run is held to the gap it was due at, for one headway",
			[]trip{{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0}},
			at(8, 9, 59), 10 * time.Minute},
		{"a headway after the window, nothing is due",
			[]trip{{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0}},
			at(8, 10, 0), 0},
		{"before the window, nothing is due",
			[]trip{{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0}},
			at(6, 59, 59), 0},
		{"two patterns every 10 minutes give every 5",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
			},
			at(7, 30, 0), 5 * time.Minute},
		{"every 10 and every 15 give every 6",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(15*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
			},
			at(7, 30, 0), 6 * time.Minute},
		{"a pattern that ended a headway ago no longer adds",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(7, 30, 0))}, 0},
			},
			at(7, 40, 0), 10 * time.Minute},
		{"a row that holds the time comes before one that has just ended",
			[]trip{{[]rider.Frequency{every(30*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(5*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0}},
			at(8, 1, 0), 5 * time.Minute},
		{"after two rows end, the later one's gap, not the longer one's",
			[]trip{{[]rider.Frequency{every(30*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(5*time.Minute, hms(8, 0, 0), hms(8, 10, 0))}, 0}},
			at(8, 12, 0), 5 * time.Minute},
		{"the later row wins in whatever order the rows come",
			[]trip{{[]rider.Frequency{every(5*time.Minute, hms(8, 0, 0), hms(8, 10, 0)), every(30*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0}},
			at(8, 12, 0), 5 * time.Minute},
		// 1 / (1/600 + 1/800) is 342.86 seconds.
		{"to the nearest second",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(800*time.Second, hms(7, 0, 0), hms(8, 0, 0))}, 0},
			},
			at(7, 30, 0), 343 * time.Second},
		{"a row with no headway sets no rate",
			[]trip{{[]rider.Frequency{{StartTime: hms(7, 0, 0), EndTime: hms(8, 0, 0)}}, 0}},
			at(7, 30, 0), 0},
		{"a second at the least, however many trips are due every second",
			[]trip{
				{[]rider.Frequency{every(time.Second, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(time.Second, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(time.Second, hms(7, 0, 0), hms(8, 0, 0))}, 0},
			},
			at(7, 30, 0), time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s stopSchedule
			for _, tr := range tc.trips {
				s.addTrip(tr.rows, reportDayStart, tr.offset)
			}
			assert.Equal(t, tc.want, s.gapAt(tc.at))
		})
	}
}

func TestStopSchedule_Stretches(t *testing.T) {
	at := func(h, m int) time.Time { return reportDayStart.Add(hms(h, m, 0)) }
	tests := []struct {
		name  string
		trips [][]rider.Frequency
		want  []timeSpan
	}{
		{"none", nil, []timeSpan{}},
		{"a window held a headway past its end",
			[][]rider.Frequency{{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}},
			[]timeSpan{{at(7, 0), at(8, 10)}}},
		{"windows apart are stretches apart, in time order",
			[][]rider.Frequency{{every(10*time.Minute, hms(19, 0, 0), hms(20, 0, 0)), every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}},
			[]timeSpan{{at(7, 0), at(8, 10)}, {at(19, 0), at(20, 10)}}},
		{"a window that starts as the one before is held to is one stretch",
			[][]rider.Frequency{{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(30*time.Minute, hms(8, 10, 0), hms(9, 0, 0))}},
			[]timeSpan{{at(7, 0), at(9, 30)}}},
		{"one that starts a minute later is not",
			[][]rider.Frequency{{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(30*time.Minute, hms(8, 11, 0), hms(9, 0, 0))}},
			[]timeSpan{{at(7, 0), at(8, 10)}, {at(8, 11), at(9, 30)}}},
		{"two trips' windows overlap into one",
			[][]rider.Frequency{
				{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))},
				{every(15*time.Minute, hms(7, 30, 0), hms(7, 45, 0))},
			},
			[]timeSpan{{at(7, 0), at(8, 10)}}},
		{"a window inside a longer one held further keeps the longer end",
			[][]rider.Frequency{
				{every(30*time.Minute, hms(7, 0, 0), hms(8, 0, 0))},
				{every(5*time.Minute, hms(7, 10, 0), hms(7, 20, 0))},
			},
			[]timeSpan{{at(7, 0), at(8, 30)}}},
		{"a row with no headway is no stretch",
			[][]rider.Frequency{{{StartTime: hms(7, 0, 0), EndTime: hms(8, 0, 0)}}},
			[]timeSpan{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s stopSchedule
			for _, rows := range tc.trips {
				s.addTrip(rows, reportDayStart, 0)
			}
			assert.Equal(t, tc.want, s.stretches())
		})
	}
}

// TestStopSchedule_AddTripShiftsTheWindows: a trip's windows reach a stop
// later in its pattern by the pattern's time to it.
func TestStopSchedule_AddTripShiftsTheWindows(t *testing.T) {
	var s stopSchedule
	s.addTrip([]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, reportDayStart, 5*time.Minute)
	assert.Equal(t, []timeSpan{{reportDayStart.Add(hms(7, 5, 0)), reportDayStart.Add(hms(8, 15, 0))}}, s.stretches())
}
