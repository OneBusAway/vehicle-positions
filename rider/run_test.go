package rider

import (
	"fmt"
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clock is a time of the 20260902 service day, hours going past 24 after
// midnight as GTFS writes them.
func clock(h, m, s int) time.Duration {
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

func TestTripInfo_Run(t *testing.T) {
	tz, err := time.LoadLocation(fixtureTimezone)
	require.NoError(t, err)
	dayStart, err := ServiceDayStart("20260902", tz)
	require.NoError(t, err)

	headway := []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute}}
	timetable := []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased}}
	// Run by headway in the morning peak and to a timetable after it.
	mixed := []gtfs.Frequency{
		{StartTime: clock(6, 0, 0), EndTime: clock(9, 0, 0), Headway: 5 * time.Minute},
		{StartTime: clock(9, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased},
	}
	noHeadway := []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), ExactTimes: gtfs.ScheduleBased}}

	tests := []struct {
		name        string
		frequencies []gtfs.Frequency
		started     time.Duration
		wantStart   string
		wantHeadway bool
	}{
		{"timetabled trip", nil, clock(8, 3, 17), "", false},
		{"headway run starts when it started", headway, clock(8, 3, 17) + 600*time.Millisecond, "08:03:17", true},
		{"headway run after its last window", headway, clock(24, 30, 0), "24:30:00", true},
		{"headway run before its service day", headway, -10 * time.Minute, "", true},
		{"exact_times=1 run takes the departure before", timetable, clock(8, 3, 17), "08:00:00", false},
		{"exact_times=1 run takes the departure after", timetable, clock(8, 6, 0), "08:10:00", false},
		{"exact_times=1 run before its window", timetable, clock(5, 40, 0), "06:00:00", false},
		{"exact_times=1 run after its window takes the last departure", timetable, clock(22, 30, 0), "21:50:00", false},
		{"mixed trip, headway window", mixed, clock(8, 59, 59), "08:59:59", true},
		{"mixed trip, timetable window from its start_time", mixed, clock(9, 0, 0), "09:00:00", false},
		{"mixed trip, timetable window", mixed, clock(10, 4, 0), "10:00:00", false},
		{"mixed trip, before both windows", mixed, clock(5, 50, 0), "05:50:00", true},
		{"mixed trip, after both windows", mixed, clock(22, 40, 0), "21:50:00", false},
		{"exact_times=1 row with no headway", noHeadway, clock(8, 3, 17), "06:00:00", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			trip := &TripInfo{frequencies: tc.frequencies}
			start, headway := trip.Run("20260902", tz, dayStart.Add(tc.started))
			assert.Equal(t, tc.wantStart, start)
			assert.Equal(t, tc.wantHeadway, headway)
		})
	}
}

// A run whose start the caller does not know (a vehicle seeded at startup) or
// whose service date is not one says nothing of which row it is in, so it
// gets no start_time and the trip's own headway flag.
func TestTripInfo_Run_UnknownStart(t *testing.T) {
	tz, err := time.LoadLocation(fixtureTimezone)
	require.NoError(t, err)
	// To a timetable in the morning and by headway after.
	trip := &TripInfo{FrequencyBased: true, frequencies: []gtfs.Frequency{
		{StartTime: clock(6, 0, 0), EndTime: clock(9, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased},
		{StartTime: clock(9, 0, 0), EndTime: clock(22, 0, 0), Headway: 5 * time.Minute},
	}}
	started := time.Date(2026, 9, 2, 8, 4, 0, 0, tz)

	for name, run := range map[string]func() (string, bool){
		"no start":        func() (string, bool) { return trip.Run("20260902", tz, time.Time{}) },
		"no service date": func() (string, bool) { return trip.Run("", tz, started) },
	} {
		start, headway := run()
		assert.Empty(t, start, name)
		assert.True(t, headway, name)
	}
}

// headwayT1 makes the fixture's T1 a trip run every 10 minutes by headway all
// day.
func headwayT1(f *aggFixture) {
	f.t1.FrequencyBased = true
	f.t1.frequencies = []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute}}
}

// startSeconds parses a GTFS start_time into seconds since the start of the
// service day.
func startSeconds(t *testing.T, start string) int {
	t.Helper()
	var h, m, s int
	_, err := fmt.Sscanf(start, "%d:%d:%d", &h, &m, &s)
	require.NoError(t, err, start)
	return h*3600 + m*60 + s
}

// A rider boards along the way, so the riders' run starts when the schedule
// says it would have left its first stop to be where they put it: T1 leaves
// at 08:00 and the rider walks it on time.
func TestAggregator_RiderRunStartsAtItsFirstDeparture(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("r1", "rider-a", TierTrusted)
	now := f.walkOffset(t, "r1", 300, 400, 10, 0)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.True(t, est[0].FrequencyBased)
	assert.InDelta(t, 8*3600, startSeconds(t, est[0].StartTime), 1, est[0].StartTime)
}

// A run in an exact_times=1 window keeps a timetable: SCHEDULED, at the
// window's departure nearest when it left its first stop.
func TestAggregator_RiderRunToATimetableIsScheduled(t *testing.T) {
	f := newAggFixture(t)
	f.t1.FrequencyBased = true
	f.t1.frequencies = []gtfs.Frequency{
		{StartTime: clock(6, 0, 0), EndTime: clock(7, 30, 0), Headway: 5 * time.Minute},
		{StartTime: clock(7, 30, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased},
	}
	f.addSession("r1", "rider-a", TierTrusted)
	now := f.walkOffset(t, "r1", 300, 400, 10, 0)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.False(t, est[0].FrequencyBased)
	assert.Equal(t, "08:00:00", est[0].StartTime)
}

// The run's start_time is fixed when the riders' trip is first published,
// however its later riders put it, until the last ride on the trip is
// removed.
func TestAggregator_RiderRunStartStaysUntilTheTripsLastRide(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("r1", "rider-a", TierTrusted)
	now := f.walkOffset(t, "r1", 300, 400, 10, 0)
	first := f.agg.Estimates(now, noCover)
	require.Len(t, first, 1)

	// A rider on the same trip three minutes behind it, then the first one
	// gets off.
	f.addSession("r2", "rider-b", TierTrusted)
	now = f.walkOffset(t, "r2", 300, 400, 10, 3*time.Minute)
	f.agg.Remove("r1")
	later := f.agg.Estimates(now, noCover)
	require.Len(t, later, 1)
	assert.Equal(t, first[0].StartTime, later[0].StartTime)

	// Once nobody rides it, the next riders' run gets its own start.
	f.agg.Remove("r2")
	f.addSession("r3", "rider-c", TierTrusted)
	now = f.walkOffset(t, "r3", 300, 400, 10, 3*time.Minute)
	next := f.agg.Estimates(now, noCover)
	require.Len(t, next, 1)
	assert.InDelta(t, 8*3600+180, startSeconds(t, next[0].StartTime), 1, next[0].StartTime)
}
