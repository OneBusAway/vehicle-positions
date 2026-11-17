package rider

import (
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// estimateStarts is the start_time, in seconds into the service day, of each
// trip estimate the aggregator has at now, in the order Estimates gives them.
func estimateStarts(t *testing.T, f *aggFixture, now time.Time) []int {
	t.Helper()
	var starts []int
	for _, est := range f.agg.Estimates(now, noCover) {
		starts = append(starts, startSeconds(t, est.StartTime))
	}
	return starts
}

// assertStartsNear checks that got are want, each within a second.
func assertStartsNear(t *testing.T, want, got []int) {
	t.Helper()
	require.Len(t, got, len(want), "got %v", got)
	for i := range want {
		assert.InDelta(t, want[i], got[i], 1, "run %d of %v", i, got)
	}
}

// Two runs of one headway trip with riders on both at once: the run that
// left at 08:00 is 500 m on when riders on the one that left at 08:05 start
// reporting. Each run is its own estimate with its own start, and each start
// stays put as the runs move on and as riders join and leave them.
func TestAggregator_ConcurrentRunsOfAHeadwayTripStayApart(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	early, late := 8*3600, 8*3600+5*60

	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("b1", "rider-b1", TierTrusted)
	f.walkOffset(t, "a1", 500, 600, 10, 0)
	now := f.walkOffset(t, "b1", 0, 100, 10, 5*time.Minute)
	assertStartsNear(t, []int{early, late}, estimateStarts(t, f, now))

	f.walkOffset(t, "a1", 610, 700, 10, 0)
	now = f.walkOffset(t, "b1", 110, 200, 10, 5*time.Minute)
	assertStartsNear(t, []int{early, late}, estimateStarts(t, f, now))

	// A second rider boards the early run and the first gets off.
	f.addSession("a2", "rider-a2", TierTrusted)
	f.walkOffset(t, "a2", 690, 750, 10, 0)
	f.agg.Remove("a1")
	now = f.walkOffset(t, "b1", 210, 260, 10, 5*time.Minute)
	assertStartsNear(t, []int{early, late}, estimateStarts(t, f, now))
}

// A trip run by timetable runs once a day, so its riders are one group
// however far apart they are: two of them 500 m apart are not two vehicles of
// that trip, and no position is published until they agree.
func TestAggregator_TimetabledTripIsOneRun(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("b1", "rider-b1", TierTrusted)
	f.walkOffset(t, "a1", 500, 600, 10, 0)
	now := f.walkOffset(t, "b1", 0, 100, 10, 5*time.Minute)

	assert.Empty(t, f.agg.Estimates(now, noCover))
	reported, riders := f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.False(t, reported)
	assert.Equal(t, 2, riders)
}

// The trip status says the riders' position is published when any run of
// the trip is.
func TestAggregator_TripStatusOfConcurrentRuns(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("b1", "rider-b1", TierNew)
	f.walkOffset(t, "a1", 500, 600, 10, 0)
	now := f.walkOffset(t, "b1", 0, 100, 10, 5*time.Minute)

	reported, riders := f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.True(t, reported, "the early run's trusted rider is published")
	assert.Equal(t, 2, riders)
}

// Two runs that come out at the same start_time are one trip instance to
// GTFS-RT. Runs to a half-hourly timetable that left at 08:05 and 08:10 both
// round to the 08:00 departure, and the run more riders are on is the one
// published.
func TestAggregator_RunsAtOneStartTimeArePublishedOnce(t *testing.T) {
	f := newAggFixture(t)
	f.t1.frequencies = []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 30 * time.Minute, ExactTimes: gtfs.ScheduleBased}}
	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("a2", "rider-a2", TierTrusted)
	f.addSession("b1", "rider-b1", TierTrusted)
	f.walkOffset(t, "a1", 500, 600, 10, 5*time.Minute)
	f.walkOffset(t, "a2", 500, 600, 10, 5*time.Minute)
	now := f.walkOffset(t, "b1", 0, 100, 10, 10*time.Minute)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, "08:00:00", est[0].StartTime)
	assert.Equal(t, 2, est[0].Riders)
}

// Of the kept runs an estimate could be, it takes the one whose start is
// nearest its own. The 08:00 run was last placed 400 m on; the 08:09 run's
// riders, now 300 m on, are within reach of both, nearer the 08:00 run's
// last place but started when the 08:09 run did.
func TestAggregator_RunTakesTheKeptStartNearestItsOwn(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("x1", "rider-x1", TierTrusted)
	now := f.walkOffset(t, "x1", 300, 400, 10, 0)
	assertStartsNear(t, []int{8 * 3600}, estimateStarts(t, f, now))
	_, ok := f.agg.End("x1", EndArrived, now)
	require.True(t, ok)

	f.addSession("y1", "rider-y1", TierTrusted)
	now = f.walkOffset(t, "y1", 0, 60, 10, 9*time.Minute)
	assertStartsNear(t, []int{8*3600 + 9*60}, estimateStarts(t, f, now))
	now = f.walkOffset(t, "y1", 70, 300, 10, 9*time.Minute)
	assertStartsNear(t, []int{8*3600 + 9*60}, estimateStarts(t, f, now))
}

// A kept run that no estimate has placed for idleTimeout has no riders left,
// and a run that comes along later, within its reach, gets its own start.
func TestAggregator_IdleRunIsLetGo(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("x1", "rider-x1", TierTrusted)
	now := f.walkOffset(t, "x1", 0, 100, 10, 0)
	assertStartsNear(t, []int{8 * 3600}, estimateStarts(t, f, now))
	_, ok := f.agg.End("x1", EndArrived, now)
	require.True(t, ok)

	f.addSession("y1", "rider-y1", TierTrusted)
	now = f.walkOffset(t, "y1", 200, 250, 10, 15*time.Minute)
	assertStartsNear(t, []int{8*3600 + 15*60}, estimateStarts(t, f, now))
}

// Each kept run goes to one estimate at most. Riders 50 m into the run
// behind could be the run ahead, kept 100 m on before it moved another 160 m,
// but that run's own riders are nearer its start, so the run behind gets its
// own start rather than sharing it.
func TestAggregator_KeptRunGoesToOneRun(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("a1", "rider-a1", TierTrusted)
	now := f.walkOffset(t, "a1", 0, 100, 10, 0)
	assertStartsNear(t, []int{8 * 3600}, estimateStarts(t, f, now))

	f.addSession("b1", "rider-b1", TierTrusted)
	f.walkOffset(t, "a1", 110, 260, 10, 0)
	now = f.walkOffset(t, "b1", 0, 50, 10, 125*time.Second)
	assertStartsNear(t, []int{8 * 3600, 8*3600 + 124}, estimateStarts(t, f, now))
}

// Riders on one bus can be well apart along the shape: the trim keeps two
// riders up to twice outlierDistance apart in one estimate. Two trusted
// riders on one run 150 m apart, with a few seconds between their latest
// points, are one vehicle.
func TestAggregator_RidersOfOneRunAreOneVehicle(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("a2", "rider-a2", TierTrusted)
	f.walkOffset(t, "a1", 400, 550, 10, 0)
	now := f.walkOffset(t, "a2", 300, 400, 10, 85*time.Second)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, 2, est[0].Riders)
}

// Riders are on different runs only if they are far apart as their points
// stand and their runs' vehicles are far apart now, each run's newest point
// carried on at its speed, no faster than MaxSpeed and no later than now.
func TestTripGroup_Runs(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	at := f.base.Add(5 * time.Minute)
	rider := func(id string, along, speed float64, age time.Duration) estimateMember {
		return estimateMember{rideID: id, riderID: id, along: along, speed: speed, timestamp: at.Add(-age)}
	}

	tests := []struct {
		name    string
		members []estimateMember
		want    [][]string
	}{
		{"500 m apart now are two runs",
			[]estimateMember{rider("a", 600, 1.7, 0), rider("b", 100, 1.7, 0)},
			[][]string{{"b"}, {"a"}}},
		{"240 m back on a point 10 s older at 15 m/s is 90 m back now",
			[]estimateMember{rider("a", 500, 15, 0), rider("b", 260, 15, 10*time.Second)},
			[][]string{{"b", "a"}}},
		{"240 m apart now are two runs",
			[]estimateMember{rider("a", 500, 15, 0), rider("b", 260, 15, 0)},
			[][]string{{"b"}, {"a"}}},
		{"a point with no speed is taken as it stands",
			[]estimateMember{rider("a", 100, 1.7, 0), rider("b", 340, -1, 60*time.Second)},
			[][]string{{"a"}, {"b"}}},
		{"a speed past MaxSpeed is taken as MaxSpeed",
			[]estimateMember{rider("a", 600, 1.7, 0), rider("b", 100, 100, 5*time.Second)},
			[][]string{{"b"}, {"a"}}},
		{"no rider is carried past now by a clock that runs ahead",
			[]estimateMember{rider("a", 600, 1.7, -30*time.Second), rider("b", 300, 10, 0)},
			[][]string{{"b"}, {"a"}}},
		{"a point from a clock that runs ahead is not carried back",
			[]estimateMember{rider("a", 600, 15, -30*time.Second), rider("b", 350, 1.7, 0)},
			[][]string{{"b"}, {"a"}}},
		{"a point taken at speed before the vehicle stopped does not set its rider apart",
			[]estimateMember{rider("a", 500, 0, 0), rider("b", 480, 15, 60*time.Second)},
			[][]string{{"b", "a"}}},
		{"a point taken at speed before the vehicle stopped is not carried into the run ahead",
			[]estimateMember{rider("B", 100, 0, 0), rider("A", 0, 15, 60*time.Second), rider("C", 700, 0, 0)},
			[][]string{{"A", "B"}, {"C"}}},
		{"a lone point carried on joins the run it reaches",
			[]estimateMember{rider("X", 0, 0, 0), rider("Y", 300, 15, 35*time.Second), rider("Z", 800, 0, 0)},
			[][]string{{"X"}, {"Y", "Z"}}},
		{"a lone point carried on past the run ahead is another run",
			[]estimateMember{rider("A", 0, 15, 80*time.Second), rider("C", 700, 0, 0)},
			[][]string{{"A"}, {"C"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got [][]string
			for _, run := range (&tripGroup{trip: f.t1, members: tc.members}).runs(DefaultThresholds(), at) {
				var ids []string
				for _, m := range run.members {
					ids = append(ids, m.riderID)
				}
				got = append(got, ids)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// Two runs at one start_time with as many riders each: the one with the newer
// point is published. The run ahead's rider reported last.
func TestAggregator_RunsAtOneStartTimeTieGoesToTheNewerPoint(t *testing.T) {
	f := newAggFixture(t)
	f.t1.frequencies = []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 30 * time.Minute, ExactTimes: gtfs.ScheduleBased}}
	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("b1", "rider-b1", TierTrusted)
	f.walkOffset(t, "b1", 0, 90, 10, 10*time.Minute)
	now := f.walkOffset(t, "a1", 500, 600, 10, 5*time.Minute)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, "08:00:00", est[0].StartTime)
	assert.Equal(t, f.onSchedulePoint(600, 5*time.Minute).Timestamp, est[0].Timestamp)
}

// A rider whose point is a minute older than the rest, 240 m back, is where
// the rest are once carried on at the point's speed: one run, whose estimate
// trims that rider as it always would, not a second vehicle.
func TestAggregator_OlderPointIsCarriedOnToItsRun(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("a1", "rider-a1", TierTrusted)
	f.addSession("a2", "rider-a2", TierTrusted)
	f.addSession("a3", "rider-a3", TierTrusted)
	f.walkOffset(t, "a3", 200, 300, 10, 90*time.Second)
	f.walkOffset(t, "a2", 440, 540, 10, 0)
	now := f.walkOffset(t, "a1", 450, 550, 10, 0)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, 2, est[0].Riders)
}
