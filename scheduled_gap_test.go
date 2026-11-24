package main

import (
	"slices"
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
		{"a pattern that ran alongside still adds for a headway after it ends",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(7, 30, 0))}, 0},
			},
			at(7, 35, 0), 5 * time.Minute},
		{"a trip that takes over as another's window ends is due alone",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 5, 0), 10 * time.Minute},
		{"and so is one that takes over a little after it ends",
			[]trip{
				{[]rider.Frequency{every(30*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(5*time.Minute, hms(8, 10, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 12, 0), 5 * time.Minute},
		// Down the route the earlier trip reaches the stop 12 minutes after
		// leaving the first, from 07:12 to 08:12, and the next one 10
		// minutes after, from 08:10.
		{"a trip quicker to the stop takes over from its first run, before the window of the one before ends",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 12 * time.Minute},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 10 * time.Minute},
			},
			at(8, 11, 0), 10 * time.Minute},
		{"and holds it no longer after it ends",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 12 * time.Minute},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 10 * time.Minute},
			},
			at(8, 15, 0), 10 * time.Minute},
		{"a trip that began a second less than a headway before another's window ended takes over",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 50, 1), hms(9, 0, 0))}, 0},
			},
			at(7, 55, 0), 10 * time.Minute},
		{"one that began a headway before shares the stop",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 50, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 55, 0), 5 * time.Minute},
		{"and still adds to it for a headway after it ends",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 50, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 5, 0), 5 * time.Minute},
		{"as does one that began half an hour before",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 30, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 5, 0), 5 * time.Minute},
		{"every 5 minutes taking over 7 minutes before every 30 ends, inside the 30-minute headway",
			[]trip{
				{[]rider.Frequency{every(30*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 12 * time.Minute},
				{[]rider.Frequency{every(5*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 5 * time.Minute},
			},
			at(8, 20, 0), 5 * time.Minute},
		// 1 / (1/300 + 1/1800) is 257.14 seconds.
		{"every 30 minutes beginning 7 minutes before every 5 ends, more than its headway, shares the stop",
			[]trip{
				{[]rider.Frequency{every(5*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 12 * time.Minute},
				{[]rider.Frequency{every(30*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 5 * time.Minute},
			},
			at(8, 10, 0), 257 * time.Second},
		{"a pattern sharing the stop throughout still adds to a trip that takes over",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 12 * time.Minute},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 10 * time.Minute},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 11, 0), 5 * time.Minute},
		{"a trip that takes over is taken over in turn",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 12 * time.Minute},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 10, 0))}, 10 * time.Minute},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 10, 0), hms(9, 0, 0))}, 8 * time.Minute},
			},
			at(8, 19, 0), 10 * time.Minute},
		{"of two runs shorter than a headway, the later takes over, so one is always due",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 1, 0), hms(8, 6, 0))}, 0},
			},
			at(8, 2, 0), 10 * time.Minute},
		{"two that begin together share the stop",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 6, 0))}, 0},
			},
			at(8, 2, 0), 5 * time.Minute},
		{"a trip's next row does not take over from a pattern it has run alongside",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(7, 55, 0)), every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 56, 0), 5 * time.Minute},
		{"a pattern that begins in the last headway of a trip's first row shares the stop when the trip runs on in a second",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 5 * time.Minute},
		{"as it does when the trip is written as one row",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(9, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 5 * time.Minute},
		{"and as one trip a row",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 5 * time.Minute},
		{"and as three trips, the middle one shorter than a headway",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 4, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 4, 0), hms(9, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 5 * time.Minute},
		{"two patterns each written one trip a row still share the stop after their next trips begin",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 3, 0), 5 * time.Minute},
		{"a trip that two trips carry on from as it ends is due no longer",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 30, 0))}, 0},
			},
			at(8, 3, 0), 5 * time.Minute},
		{"and two trips that one trip carries on from as they end are both due no longer",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 3, 0), 10 * time.Minute},
		{"a row that begins as a trip ends carries it on only when its own run begins there, so one is always due",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(10*time.Minute, hms(8, 0, 0), hms(8, 2, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 58, 0), hms(8, 0, 0))}, 0},
			},
			at(8, 1, 0), 10 * time.Minute},
		{"of two trips that carry a run on and end together, the one due more often ends it",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(30*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
				{[]rider.Frequency{every(5*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 5 * time.Minute},
		{"whichever of them comes first",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(5*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
				{[]rider.Frequency{every(30*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 5 * time.Minute},
		{"a pattern that begins in a trip's last headway takes over though its own next row begins as the trip ends",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 55, 0), hms(8, 0, 0)), every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
			},
			at(7, 58, 0), 10 * time.Minute},
		{"a row written to end before it begins is not carried on by a trip that began first, so one is still due",
			[]trip{
				{[]rider.Frequency{every(30*time.Minute, hms(8, 10, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(30*time.Minute, hms(8, 0, 0), hms(8, 5, 0))}, 0},
			},
			at(8, 12, 0), 30 * time.Minute},
		{"a trip that takes over keeps the one before out for its own last headway",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(7, 58, 0), hms(8, 2, 0))}, 0},
			},
			at(8, 3, 0), 10 * time.Minute},
		{"a trip with a row that ends as it begins still takes over",
			[]trip{
				{[]rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, 0},
				{[]rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(8, 0, 0)), every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}, 0},
			},
			at(8, 5, 0), 10 * time.Minute},
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

// TestStopService_GapsAcrossATakeover: service every 10 minutes written as
// two trips, one up to 08:00 and one from then. Its 08:00 vehicle, on time
// and 10 minutes after the 07:50, is on the gap, as it is when the feed
// writes the two windows as rows of one trip, not long against every 5.
func TestStopService_GapsAcrossATakeover(t *testing.T) {
	passings := []stopPassing{{at: reportDayStart.Add(hms(7, 50, 6)), run: 1}, {at: reportDayStart.Add(hms(8, 0, 6)), run: 2}}
	rows := []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}

	oneTrip := stopService{passings: passings}
	oneTrip.schedule.addTrip(rows, reportDayStart, 0)
	twoTrips := stopService{passings: passings}
	twoTrips.schedule.addTrip(rows[:1], reportDayStart, 0)
	twoTrips.schedule.addTrip(rows[1:], reportDayStart, 0)

	for name, s := range map[string]stopService{"one trip": oneTrip, "two trips": twoTrips} {
		gaps := s.gaps()
		if assert.Len(t, gaps, 1, name) {
			assert.Equal(t, 10*time.Minute, gaps[0].scheduled, name)
			assert.False(t, gaps[0].long(), name)
		}
	}
}

// TestStopService_GapsAcrossAnOverlappingTakeover: the same service every 10
// minutes written as two trips, the later quicker to the stop: the first
// reaches it 12 minutes after leaving the first stop, so its window there is
// 07:12 to 08:12, and the next 10 minutes after, from 08:10. Its 08:10
// vehicle, on time and 8 minutes after the first trip's last, and the 08:20
// after it, are on the gap of every 10 minutes, as with one trip, not long
// against every 5.
func TestStopService_GapsAcrossAnOverlappingTakeover(t *testing.T) {
	passings := []stopPassing{
		{at: reportDayStart.Add(hms(7, 52, 6)), run: 1},
		{at: reportDayStart.Add(hms(8, 2, 6)), run: 2},
		{at: reportDayStart.Add(hms(8, 10, 6)), run: 3},
		{at: reportDayStart.Add(hms(8, 20, 6)), run: 4},
	}
	rows := []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), every(10*time.Minute, hms(8, 0, 0), hms(9, 0, 0))}

	oneTrip := stopService{passings: passings}
	oneTrip.schedule.addTrip(rows, reportDayStart, 12*time.Minute)
	twoTrips := stopService{passings: passings}
	twoTrips.schedule.addTrip(rows[:1], reportDayStart, 12*time.Minute)
	twoTrips.schedule.addTrip(rows[1:], reportDayStart, 10*time.Minute)

	for name, s := range map[string]stopService{"one trip": oneTrip, "two trips": twoTrips} {
		gaps := s.gaps()
		if assert.Len(t, gaps, 3, name) {
			assert.Equal(t, []time.Duration{10 * time.Minute, 8 * time.Minute, 10 * time.Minute},
				[]time.Duration{gaps[0].gap, gaps[1].gap, gaps[2].gap}, name)
			for _, g := range gaps {
				assert.Equal(t, 10*time.Minute, g.scheduled, "%s at %s", name, g.at.Format(time.TimeOnly))
				assert.False(t, g.long(), "%s at %s", name, g.at.Format(time.TimeOnly))
			}
		}
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

// departing are times on the report fixture's service day.
func departing(times ...time.Duration) []time.Time {
	out := make([]time.Time, 0, len(times))
	for _, at := range times {
		out = append(out, reportDayStart.Add(at))
	}
	return out
}

// everyFrom is a departure every spacing from first up to and including last.
func everyFrom(spacing, first, last time.Duration) []time.Duration {
	var out []time.Duration
	for at := first; at <= last; at += spacing {
		out = append(out, at)
	}
	return out
}

func TestStopSchedule_TimetableGapAt(t *testing.T) {
	at := func(h, m, s int) time.Time { return reportDayStart.Add(hms(h, m, s)) }
	// The spacing is 6 minutes up to 07:06, then 10.
	sixThenTen := []time.Duration{hms(7, 0, 0), hms(7, 6, 0), hms(7, 16, 0), hms(7, 26, 0)}
	tests := []struct {
		name       string
		departures []time.Duration
		trip       []rider.Frequency
		at         time.Time
		want       time.Duration
	}{
		{"every 10 minutes", everyFrom(10*time.Minute, hms(7, 0, 0), hms(7, 30, 0)), nil, at(7, 14, 0), 10 * time.Minute},
		{"every 12 minutes is frequent", []time.Duration{hms(7, 0, 0), hms(7, 12, 0)}, nil, at(7, 6, 0), 12 * time.Minute},
		{"12 minutes and a second apart is not", []time.Duration{hms(7, 0, 0), hms(7, 12, 1)}, nil, at(7, 6, 0), 0},
		{"a vehicle passing on time is judged against the spacing up to its departure",
			sixThenTen, nil, at(7, 6, 6), 6 * time.Minute},
		{"and still when late, up to halfway to the next departure",
			sixThenTen, nil, at(7, 10, 59), 6 * time.Minute},
		{"from halfway, the next spacing",
			sixThenTen, nil, at(7, 11, 0), 10 * time.Minute},
		{"a little early, the spacing up to the departure it is early for",
			sixThenTen, nil, at(7, 15, 0), 10 * time.Minute},
		{"from the first departure, the spacing after it",
			sixThenTen, nil, at(7, 0, 0), 6 * time.Minute},
		{"every 8 then 12 minutes: a vehicle on time after the 12 is judged against 12, not the 8 after it",
			[]time.Duration{hms(7, 0, 0), hms(7, 8, 0), hms(7, 20, 0), hms(7, 28, 0), hms(7, 40, 0)}, nil, at(7, 20, 6), 12 * time.Minute},
		{"a late last run is held to the last spacing for one spacing",
			sixThenTen, nil, at(7, 35, 59), 10 * time.Minute},
		{"a spacing after the last departure, nothing is due",
			sixThenTen, nil, at(7, 36, 0), 0},
		{"before the first departure, nothing is due",
			sixThenTen, nil, at(6, 59, 59), 0},
		{"a vehicle late after a short spacing is held to it up to halfway to a longer next one",
			[]time.Duration{hms(7, 0, 0), hms(7, 2, 0), hms(7, 14, 0)}, nil, at(7, 5, 0), 2 * time.Minute},
		{"a short last spacing late on is held to the longer one before it",
			[]time.Duration{hms(7, 0, 0), hms(7, 12, 0), hms(7, 13, 0)}, nil, at(7, 20, 0), 12 * time.Minute},
		{"a lone departure sets no gap", []time.Duration{hms(7, 35, 0)}, nil, at(7, 35, 0), 0},
		{"two trips due at the same time are one departure",
			[]time.Duration{hms(7, 0, 0), hms(7, 0, 0), hms(7, 10, 0), hms(7, 10, 0), hms(7, 20, 0)}, nil, at(7, 10, 6), 10 * time.Minute},
		{"departures in any order",
			[]time.Duration{hms(7, 26, 0), hms(7, 6, 0), hms(7, 16, 0), hms(7, 0, 0)}, nil, at(7, 6, 6), 6 * time.Minute},
		{"a trip every 10 minutes and a timetable every 10 make it every 5",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(7, 30, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(7, 14, 0), 5 * time.Minute},
		// 1 / (1/600 + 1/360) is 225 seconds.
		{"a trip every 10 minutes and a timetable every 6 make it every 3m45s",
			everyFrom(6*time.Minute, hms(7, 0, 0), hms(7, 30, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(7, 14, 0), 225 * time.Second},
		{"a timetable less often than every 12 minutes adds nothing to a trip every 10",
			everyFrom(15*time.Minute, hms(7, 0, 0), hms(7, 45, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(7, 14, 0), 10 * time.Minute},
		{"after the timetable's last spacing, the trip alone",
			[]time.Duration{hms(7, 0, 0), hms(7, 10, 0)}, []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(7, 20, 0), 10 * time.Minute},
		{"a timetable that takes over as a trip's window ends is due alone",
			everyFrom(10*time.Minute, hms(8, 0, 0), hms(8, 30, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(8, 5, 0), 10 * time.Minute},
		{"a timetable whose first departure comes before a trip's window ends takes over from it",
			everyFrom(10*time.Minute, hms(7, 58, 0), hms(8, 28, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(7, 59, 0), 10 * time.Minute},
		{"and holds it no longer after it ends",
			everyFrom(10*time.Minute, hms(7, 58, 0), hms(8, 28, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(8, 5, 0), 10 * time.Minute},
		{"a trip that begins before a timetable's last departure takes over from its last spacing",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 52, 0), hms(9, 0, 0))}, at(7, 56, 0), 10 * time.Minute},
		{"and holds it no longer after its last departure",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 52, 0), hms(9, 0, 0))}, at(8, 5, 0), 10 * time.Minute},
		{"a trip that begins partway through a frequent timetable shares the stop",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(9, 0, 0)), []rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(10, 0, 0))}, at(8, 2, 0), 5 * time.Minute},
		{"and still does in the timetable's next spacing",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(9, 0, 0)), []rider.Frequency{every(10*time.Minute, hms(8, 0, 0), hms(10, 0, 0))}, at(8, 6, 0), 5 * time.Minute},
		{"a timetable running alongside a trip does not take over as the trip's window ends",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(8, 30, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(7, 56, 0), 5 * time.Minute},
		{"and adds to it for a headway after it ends",
			everyFrom(10*time.Minute, hms(7, 0, 0), hms(8, 30, 0)), []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))}, at(8, 5, 0), 5 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s stopSchedule
			if tc.trip != nil {
				s.addTrip(tc.trip, reportDayStart, 0)
			}
			s.setTimetable(departing(tc.departures...))
			assert.Equal(t, tc.want, s.gapAt(tc.at))
		})
	}
}

func TestStopSchedule_TimetableStretches(t *testing.T) {
	at := func(h, m int) time.Time { return reportDayStart.Add(hms(h, m, 0)) }
	tests := []struct {
		name       string
		departures []time.Duration
		trip       []rider.Frequency
		want       []timeSpan
	}{
		{"none", nil, nil, []timeSpan{}},
		{"departures each within 12 minutes of the next, held the last spacing past the last",
			[]time.Duration{hms(7, 0, 0), hms(7, 10, 0), hms(7, 22, 0)}, nil, []timeSpan{{at(7, 0), at(7, 34)}}},
		{"12 minutes and a second apart is no stretch",
			[]time.Duration{hms(7, 0, 0), hms(7, 12, 1)}, nil, []timeSpan{}},
		{"a route frequent only in the peaks",
			slices.Concat(everyFrom(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0)), everyFrom(20*time.Minute, hms(8, 20, 0), hms(15, 40, 0)), everyFrom(10*time.Minute, hms(16, 0, 0), hms(17, 0, 0))),
			nil, []timeSpan{{at(7, 0), at(8, 10)}, {at(16, 0), at(17, 10)}}},
		{"a timetable that starts as a trip's window is held to is one stretch with it",
			[]time.Duration{hms(8, 10, 0), hms(8, 20, 0), hms(8, 30, 0)}, []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))},
			[]timeSpan{{at(7, 0), at(8, 40)}}},
		{"one apart from it is not",
			[]time.Duration{hms(9, 0, 0), hms(9, 10, 0)}, []rider.Frequency{every(10*time.Minute, hms(7, 0, 0), hms(8, 0, 0))},
			[]timeSpan{{at(7, 0), at(8, 10)}, {at(9, 0), at(9, 20)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s stopSchedule
			if tc.trip != nil {
				s.addTrip(tc.trip, reportDayStart, 0)
			}
			s.setTimetable(departing(tc.departures...))
			assert.Equal(t, tc.want, s.stretches())
		})
	}
}

// TestStopSchedule_SetTimetableWindows: each frequent spacing holds from
// halfway after the departure before it to halfway to the next, the first of
// a run of them from its first departure and the last up to its last, so a
// timetable's windows meet without overlapping, as windowAt needs. A
// departure with no other within 12 minutes has none.
func TestStopSchedule_SetTimetableWindows(t *testing.T) {
	at := func(h, m int) time.Time { return reportDayStart.Add(hms(h, m, 0)) }
	var s stopSchedule
	s.setTimetable(departing(hms(7, 0, 0), hms(7, 6, 0), hms(7, 16, 0), hms(7, 26, 0), hms(8, 0, 0), hms(9, 0, 0), hms(9, 10, 0)))
	assert.Equal(t, []stopWindow{
		{at(7, 0), at(7, 11), 6 * time.Minute},
		{at(7, 11), at(7, 21), 10 * time.Minute},
		{at(7, 21), at(7, 26), 10 * time.Minute},
		{at(9, 0), at(9, 10), 10 * time.Minute},
	}, s.timetable)
	assert.Equal(t, departing(hms(7, 0, 0), hms(7, 6, 0), hms(7, 16, 0), hms(7, 26, 0), hms(9, 0, 0), hms(9, 10, 0)), s.timetabled)
}

// TestStopSchedule_TimetabledAt: a departure is part of the timetable's
// frequent service when the one before or after it is within 12 minutes.
func TestStopSchedule_TimetabledAt(t *testing.T) {
	var s stopSchedule
	s.setTimetable(departing(hms(8, 30, 0), hms(7, 0, 0), hms(6, 30, 0), hms(7, 10, 0), hms(7, 20, 0), hms(7, 32, 0), hms(7, 45, 0)))
	tests := []struct {
		name string
		at   time.Duration
		want bool
	}{
		{"30 minutes before the next", hms(6, 30, 0), false},
		{"10 minutes before the next", hms(7, 0, 0), true},
		{"10 minutes either side", hms(7, 10, 0), true},
		{"12 minutes after the one before", hms(7, 32, 0), true},
		{"13 minutes after the one before and 45 before the next", hms(7, 45, 0), false},
		{"45 minutes after the one before, the last", hms(8, 30, 0), false},
		{"not a departure", hms(7, 5, 0), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, s.timetabledAt(reportDayStart.Add(tc.at)))
		})
	}

	var same stopSchedule
	same.setTimetable(departing(hms(7, 35, 0), hms(7, 35, 0)))
	assert.False(t, same.timetabledAt(reportDayStart.Add(hms(7, 35, 0))), "two trips due at the same time, alone, are one departure")
	assert.Empty(t, same.stretches())
}
