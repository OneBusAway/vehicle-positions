package rider

import (
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// estimateStart is the start_time, in seconds into the service day, of the
// one trip estimate the aggregator has at now.
func estimateStart(t *testing.T, f *aggFixture, now time.Time) int {
	t.Helper()
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	return startSeconds(t, est[0].StartTime)
}

// A first stop with a dwell: T1 arrives there at 07:58 and leaves at 08:00,
// and the run starts when it leaves.
func TestAggregator_RiderRunStartsAtItsFirstDepartureAfterADwell(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.t1.StopTimes[0].Arrival -= 2 * time.Minute
	f.addSession("r1", "rider-a", TierTrusted)
	now := f.walkOffset(t, "r1", 300, 400, 10, 0)

	assert.InDelta(t, 8*3600, estimateStart(t, f, now), 1)
}

// Every run of a headway trip on a service day is one TripKey, and on a busy
// route the next run's riders start before the last ride on this run ends.
// An estimate where this run's vehicle could not be is that next run, and
// gets its own start.
func TestAggregator_RiderRunStartResetsForAnotherRun(t *testing.T) {
	tests := []struct {
		name string
		// The first run's riders, then the other run's, each walking T1 from
		// a point to a point at the given lateness.
		firstFrom, firstTo float64
		firstLate          time.Duration
		otherFrom, otherTo float64
		otherLate          time.Duration
		wantFirst          int
		wantOther          int
	}{
		{
			// The run that left at 08:00 is nearly at the end of T1 when
			// riders on the one that left at 08:10 start reporting at its
			// beginning, 700 m back.
			name:      "the next run, back along the shape",
			firstFrom: 800, firstTo: 900, firstLate: 0,
			otherFrom: 100, otherTo: 200, otherLate: 10 * time.Minute,
			wantFirst: 8 * 3600, wantOther: 8*3600 + 10*60,
		},
		{
			// Riders on the run that left at 08:10 are near the start when
			// riders on one that left at 08:03 report from 700 m ahead,
			// seconds later: further than one vehicle could have gone.
			name:      "an earlier run, ahead along the shape",
			firstFrom: 100, firstTo: 200, firstLate: 10 * time.Minute,
			otherFrom: 800, otherTo: 900, otherLate: 3*time.Minute + 6*time.Second,
			wantFirst: 8*3600 + 10*60, wantOther: 8*3600 + 3*60 + 6,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAggFixture(t)
			headwayT1(f)
			f.addSession("r1", "rider-a", TierTrusted)
			now := f.walkOffset(t, "r1", tc.firstFrom, tc.firstTo, 10, tc.firstLate)
			assert.InDelta(t, tc.wantFirst, estimateStart(t, f, now), 1)

			// The first run's rider gets off but is still registered until
			// the ride's outcome is stored, so the trip keeps a ride.
			_, ok := f.agg.End("r1", EndArrived, now)
			require.True(t, ok)
			f.addSession("r2", "rider-b", TierTrusted)
			now = f.walkOffset(t, "r2", tc.otherFrom, tc.otherTo, 10, tc.otherLate)
			assert.InDelta(t, tc.wantOther, estimateStart(t, f, now), 1)
		})
	}
}

// One vehicle moving on along its run keeps its start, however far it goes
// in the time between estimates, and each estimate is held to the last.
func TestAggregator_RiderRunStartKeptAsTheRunMovesOn(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("r1", "rider-a", TierTrusted)
	now := f.walkOffset(t, "r1", 300, 400, 10, 0)
	first := estimateStart(t, f, now)

	// Three minutes and 500 m on, running a minute later than it was.
	now = f.walkOffset(t, "r1", 410, 900, 10, time.Minute)
	assert.Equal(t, first, estimateStart(t, f, now))

	// The vehicle's latest place is what the next estimate is held to: 300 m
	// back from it is another run, though it is ahead of where this one
	// started.
	_, ok := f.agg.End("r1", EndArrived, now)
	require.True(t, ok)
	f.addSession("r2", "rider-b", TierTrusted)
	now = f.walkOffset(t, "r2", 550, 600, 10, 4*time.Minute+5*time.Second)
	assert.InDelta(t, 8*3600+4*60+4, estimateStart(t, f, now), 1)
}

// Estimates lets go of the lock between finding a trip's riders and keeping
// their run's start, so the rides it found can be removed in between. No
// start is kept unless one of them is still registered, whether the trip has
// no ride left or a new one has replaced them, and the next riders on it get
// their own.
func TestAggregator_RunStartKeepsNothingWhenItsRidesHaveGone(t *testing.T) {
	gone := func(f *aggFixture) *tripGroup {
		return &tripGroup{trip: f.t1, members: []estimateMember{{rideID: "removed"}}}
	}
	key := TripKey{"T1", "20260902"}

	t.Run("no ride left", func(t *testing.T) {
		f := newAggFixture(t)
		headwayT1(f)
		f.agg.runStart(key, gone(f), 350, f.base.Add(4*time.Minute))
		assert.Empty(t, f.agg.runStarts)

		f.addSession("r1", "rider-a", TierTrusted)
		now := f.walkOffset(t, "r1", 300, 400, 10, 3*time.Minute)
		assert.InDelta(t, 8*3600+3*60, estimateStart(t, f, now), 1)
	})
	t.Run("a new ride in their place", func(t *testing.T) {
		f := newAggFixture(t)
		headwayT1(f)
		f.addSession("r1", "rider-a", TierTrusted)
		f.agg.runStart(key, gone(f), 350, f.base.Add(4*time.Minute))
		assert.Empty(t, f.agg.runStarts)

		now := f.walkOffset(t, "r1", 300, 400, 10, 3*time.Minute)
		assert.InDelta(t, 8*3600+3*60, estimateStart(t, f, now), 1)
	})
}

// Two estimates of a trip can reach runStart in either order. One older than
// the estimate kept is not taken for another run, however far back it is.
func TestAggregator_RunStartIgnoresAnOlderEstimate(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("r1", "rider-a", TierTrusted)
	g := &tripGroup{trip: f.t1, members: []estimateMember{{rideID: "r1"}}}
	key := TripKey{"T1", "20260902"}

	newer := f.agg.runStart(key, g, 900, f.base.Add(9*time.Minute))
	older := f.agg.runStart(key, g, 300, f.base.Add(3*time.Minute))
	assert.Equal(t, newer, older)
	assert.Equal(t, riderRun{start: newer, along: 900, at: f.base.Add(9 * time.Minute)}, f.agg.runStarts[key])
}

// A vehicle the riders put at its first stop is taken to be leaving it then,
// whatever the stop's dwell: T1 arrives there at 07:58 and leaves at 08:00.
func TestAggregator_RunStartAtTheFirstStopIsNow(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.t1.StopTimes[0].Arrival -= 2 * time.Minute
	f.addSession("r1", "rider-a", TierTrusted)
	g := &tripGroup{trip: f.t1, members: []estimateMember{{rideID: "r1"}}}

	at := f.base.Add(30 * time.Second)
	assert.Equal(t, at, f.agg.runStart(TripKey{"T1", "20260902"}, g, 0, at))
}

// A run counts its start_time from the service date whose frequencies.txt
// window holds its start, which can be the day before when the window runs
// past midnight, whatever the trip's one-run pattern of stop times says.
func TestIndex_RunServiceDate(t *testing.T) {
	ix := fixtureIndex(t)
	t1, ok := ix.Trip("T1") // weekdays, its pattern 08:00 to 08:10
	require.True(t, ok)
	at := func(day, h, m int) time.Time { return time.Date(2026, 9, day, h, m, 0, 0, ix.Timezone()) }

	tests := []struct {
		name    string
		windows []gtfs.Frequency
		started time.Time
		want    string
	}{
		{"in the day's window", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(26, 0, 0)}}, at(2, 8, 3), "20260902"},
		{"after midnight in the day before's window", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(26, 0, 0)}}, at(3, 0, 30), "20260902"},
		{"in both days' windows", []gtfs.Frequency{{StartTime: clock(0, 0, 0), EndTime: clock(30, 0, 0)}}, at(3, 5, 0), "20260902"},
		{"in the window of a day the trip does not run", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(26, 0, 0)}}, at(7, 0, 30), "20260907"},
		{"in no window", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0)}}, at(3, 0, 30), ix.ServiceDateFor(t1, at(3, 0, 30))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t1.frequencies = tc.windows
			assert.Equal(t, tc.want, ix.RunServiceDate(t1, tc.started))
		})
	}
	// On Saturday T1 does not run, and Friday's window is long over.
	t1.frequencies = []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(26, 0, 0)}}
	assert.Equal(t, ix.ServiceDateFor(t1, at(5, 8, 3)), ix.RunServiceDate(t1, at(5, 8, 3)))
}
