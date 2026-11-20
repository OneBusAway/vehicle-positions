package rider

import (
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifyOnRun judges a rider 250 m along fixture trip T1, 2:30 into its pattern of stop times (08:00 to 08:10), on a
// run that started runStart after the start of service day 20260902, with T1 run by rows instead of its stop times.
func verifyOnRun(t *testing.T, rows []gtfs.Frequency, runStart time.Duration) Verdict {
	t.Helper()
	ix := fixtureIndex(t)
	t1, ok := ix.Trip("T1")
	require.True(t, ok)
	trip := *t1
	trip.frequencies = rows
	dayStart, err := ServiceDayStart("20260902", ix.Timezone())
	require.NoError(t, err)
	at := dayStart.Add(runStart + 150*time.Second)
	return Verify(VerifyInput{
		Trip: &trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Point:      Point{Pos: trip.Shape.PointAt(250), Accuracy: 8, Speed: 9, Bearing: 0, Timestamp: at},
		Thresholds: DefaultThresholds(), Now: at,
	})
}

type runCase struct {
	name     string
	runStart time.Duration
	want     Outcome
	off      time.Duration // the ScheduleDeviation expected
}

func checkRuns(t *testing.T, rows []gtfs.Frequency, cases []runCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := verifyOnRun(t, rows, tc.runStart)
			assert.Equal(t, tc.want, v.Outcome, v.Reason)
			assert.InDelta(t, float64(tc.off), float64(v.ScheduleDeviation), float64(5*time.Second))
		})
	}
}

// An exact_times=0 window keeps no timetable: a run may start at any time in it, so a point fits when the run it
// implies starts in a window, give or take the slack a timetabled trip gets (15 minutes early, 90 late). The pattern's
// own times, hours before the window, say nothing.
func TestVerify_HeadwayTripIsJudgedByItsWindows(t *testing.T) {
	window := []gtfs.Frequency{{StartTime: clock(14, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute}}
	checkRuns(t, window, []runCase{
		{"a run in the window, seven hours after the pattern", clock(15, 0, 0), Matched, 0},
		{"the window's last run", clock(21, 55, 0), Matched, 0},
		{"a run starting a little before the window", clock(13, 50, 0), Matched, -10 * time.Minute},
		{"a run too early for the window", clock(13, 40, 0), OffSchedule, -20 * time.Minute},
		{"a run late after the window's end", clock(23, 20, 0), Matched, 80 * time.Minute},
		{"a run too long after the window's end", clock(23, 40, 0), OffSchedule, 100 * time.Minute},
		{"the pattern's own time, outside every window", clock(8, 0, 0), OffSchedule, -6 * time.Hour},
	})

	twoWindows := []gtfs.Frequency{
		{StartTime: clock(6, 0, 0), EndTime: clock(9, 0, 0), Headway: 10 * time.Minute},
		{StartTime: clock(16, 0, 0), EndTime: clock(20, 0, 0), Headway: 10 * time.Minute},
	}
	checkRuns(t, twoWindows, []runCase{
		{"in the evening window", clock(17, 0, 0), Matched, 0},
		{"late after the morning window", clock(9, 45, 0), Matched, 45 * time.Minute},
		{"between the windows, out of reach of both", clock(12, 0, 0), OffSchedule, 3 * time.Hour},
	})
}

// An exact_times=1 window is a timetable at its start_time and every headway after: a point is judged against the
// departures either side of the run it implies, so a run up to 90 minutes behind one still fits, as on a timetabled
// trip, though the next departure is nearer.
func TestVerify_ExactTimesTripIsJudgedByItsDepartures(t *testing.T) {
	everyTwoHours := []gtfs.Frequency{{StartTime: clock(14, 0, 0), EndTime: clock(22, 0, 0), Headway: 2 * time.Hour, ExactTimes: gtfs.ScheduleBased}}
	checkRuns(t, everyTwoHours, []runCase{
		{"just after a departure", clock(16, 5, 0), Matched, 5 * time.Minute},
		{"an hour after one, though between two", clock(15, 0, 0), Matched, time.Hour},
		{"70 minutes behind one, nearer the next", clock(17, 10, 0), Matched, 70 * time.Minute},
		{"a little ahead of one", clock(17, 50, 0), Matched, -10 * time.Minute},
		{"too far behind one and ahead of the next", clock(17, 40, 0), OffSchedule, -20 * time.Minute},
		{"too long after the last departure", clock(22, 30, 0), OffSchedule, 150 * time.Minute},
		{"the pattern's own time, before the first departure", clock(8, 0, 0), OffSchedule, -6 * time.Hour},
	})

	oneDeparture := []gtfs.Frequency{{StartTime: clock(14, 0, 0), EndTime: clock(22, 0, 0), ExactTimes: gtfs.ScheduleBased}}
	checkRuns(t, oneDeparture, []runCase{
		{"no headway: its start_time is its one departure", clock(14, 30, 0), Matched, 30 * time.Minute},
	})

	mixed := []gtfs.Frequency{
		{StartTime: clock(6, 0, 0), EndTime: clock(10, 0, 0), Headway: 10 * time.Minute},
		{StartTime: clock(16, 0, 0), EndTime: clock(20, 0, 0), Headway: 2 * time.Hour, ExactTimes: gtfs.ScheduleBased},
	}
	checkRuns(t, mixed, []runCase{
		{"in the headway window", clock(7, 3, 0), Matched, 0},
		{"at a timetabled departure", clock(18, 0, 0), Matched, 0},
	})
}

// A trip not in frequencies.txt is judged by its stop times exactly as before.
func TestVerify_TimetabledTripStillJudgedByItsStopTimes(t *testing.T) {
	checkRuns(t, nil, []runCase{
		{"on its stop times", clock(8, 0, 0), Matched, 0},
		{"seven hours after them", clock(15, 0, 0), OffSchedule, 7 * time.Hour},
		{"20 minutes ahead of them", clock(7, 40, 0), OffSchedule, -20 * time.Minute},
	})
}
