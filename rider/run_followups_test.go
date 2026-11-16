package rider

import (
	"testing"
	"time"

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
// their run's start, so the trip's last ride can be removed in between. No
// start is kept for a trip with no ride, and the next riders on it get their
// own.
func TestAggregator_RunStartKeepsNothingForATripWithNoRide(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.agg.runStart(TripKey{"T1", "20260902"}, f.t1, 350, f.base.Add(4*time.Minute))
	assert.Empty(t, f.agg.runStarts)

	f.addSession("r1", "rider-a", TierTrusted)
	now := f.walkOffset(t, "r1", 300, 400, 10, 3*time.Minute)
	assert.InDelta(t, 8*3600+3*60, estimateStart(t, f, now), 1)
}
