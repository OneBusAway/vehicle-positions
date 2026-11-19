package rider

import (
	"cmp"
	"math"
	"slices"
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
		// placed is where along the shape each rider is handed to the
		// estimate, and own where their own point puts them, which is what
		// they vouch for another rider from, for the cases that check them.
		placed, own map[string]float64
	}{
		{"500 m apart now are two runs",
			[]estimateMember{rider("a", 600, 1.7, 0), rider("b", 100, 1.7, 0)},
			[][]string{{"a"}, {"b"}}, nil, nil},
		{"240 m back on a point 10 s older at 15 m/s is 90 m back now",
			[]estimateMember{rider("a", 500, 15, 0), rider("b", 260, 15, 10*time.Second)},
			[][]string{{"a", "b"}}, nil, nil},
		{"240 m apart now are two runs",
			[]estimateMember{rider("a", 500, 15, 0), rider("b", 260, 15, 0)},
			[][]string{{"a"}, {"b"}}, nil, nil},
		{"a run whose newest point has no speed is where that point stands",
			[]estimateMember{rider("F", 500, -1, 80*time.Second), rider("N", 1000, 0, 0), rider("S", 270, 15, 28*time.Second)},
			[][]string{{"F", "S"}, {"N"}}, nil, nil},
		{"a speed past MaxSpeed is taken as MaxSpeed",
			[]estimateMember{rider("a", 600, 1.7, 0), rider("b", 100, 100, 5*time.Second)},
			[][]string{{"a"}, {"b"}}, nil, nil},
		{"no rider is carried past now by a clock that runs ahead",
			[]estimateMember{rider("a", 600, 1.7, -30*time.Second), rider("b", 300, 10, 0)},
			[][]string{{"a"}, {"b"}}, nil, nil},
		{"a run whose newest point is from a clock that runs ahead is not carried back",
			[]estimateMember{rider("F", 600, 15, -30*time.Second), rider("S", 150, 15, 40*time.Second)},
			[][]string{{"F", "S"}}, nil, nil},
		{"a point taken at speed before the vehicle stopped does not set its rider apart",
			[]estimateMember{rider("a", 500, 0, 0), rider("b", 480, 15, 60*time.Second)},
			[][]string{{"a", "b"}}, nil, nil},
		{"a point taken at speed before the vehicle stopped is not carried into the run ahead",
			[]estimateMember{rider("B", 100, 0, 0), rider("A", 0, 15, 60*time.Second), rider("C", 700, 0, 0)},
			[][]string{{"A", "B"}, {"C"}},
			map[string]float64{"B": 100, "A": 100, "C": 700},
			map[string]float64{"B": 100, "A": 0, "C": 700}},
		{"a lone point carried on joins the run it reaches",
			[]estimateMember{rider("X", 0, 0, 0), rider("Y", 300, 15, 35*time.Second), rider("Z", 800, 0, 0)},
			[][]string{{"X"}, {"Y", "Z"}}, nil, nil},
		{"a lone point carried on past the run ahead is another run",
			[]estimateMember{rider("A", 0, 15, 80*time.Second), rider("C", 700, 0, 0)},
			[][]string{{"A"}, {"C"}}, nil, nil},
		{"a stale point between two runs does not link them",
			[]estimateMember{rider("R1", 0, 0, 0), rider("S", 180, 15, 60*time.Second), rider("R2", 350, 0, 0)},
			[][]string{{"R1"}, {"R2", "S"}}, nil, nil},
		{"a lone point carried past a run joins the run it reaches beyond",
			[]estimateMember{rider("P", 0, 15, 60*time.Second), rider("Q", 400, 0, 0), rider("W", 900, 0, 0)},
			[][]string{{"P", "W"}, {"Q"}}, nil, nil},
		{"a lone point carried on joins the nearest run within reach",
			[]estimateMember{rider("F1", 400, 0, 0), rider("F2", 650, 0, 0), rider("S", 150, 15, 30*time.Second)},
			[][]string{{"F1"}, {"F2", "S"}}, nil, nil},
		{"a run's vehicle is where its newest point is carried",
			[]estimateMember{rider("M1", 500, 0, 0), rider("M2", 450, 1, 90*time.Second), rider("S", 240, 15, 32*time.Second)},
			[][]string{{"M1", "M2"}, {"S"}}, nil, nil},
		{"stale points standing together are one run with no run near them",
			[]estimateMember{rider("N", 2000, 0, 0), rider("T1", 0, 15, 60*time.Second), rider("T2", 50, 15, 70*time.Second)},
			[][]string{{"N"}, {"T1", "T2"}}, nil, nil},
		{"a run's own riders are carried to the moment a group joining it is placed at",
			[]estimateMember{rider("F", 500, 1, 80*time.Second), rider("S", 0, 15, 50*time.Second), rider("N", 1000, 0, 0)},
			[][]string{{"F", "S"}, {"N"}},
			map[string]float64{"F": 580, "S": 750, "N": 1000},
			map[string]float64{"F": 500, "S": 750, "N": 1000}},
		{"a stale group joining a run is placed where its newest point is carried",
			[]estimateMember{rider("F", 500, 0, 0), rider("S1", 0, 10, 50*time.Second), rider("S2", 50, 12, 70*time.Second), rider("S3", 60, 12, 70*time.Second)},
			[][]string{{"F", "S1", "S2", "S3"}},
			map[string]float64{"F": 500, "S1": 500, "S2": 500, "S3": 500},
			map[string]float64{"F": 500, "S1": 500, "S2": 890, "S3": 900}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Which riders share a run, whatever order runs() lists them in.
			var got [][]string
			placed, own := make(map[string]float64), make(map[string]float64)
			for _, run := range (&tripGroup{trip: f.t1, members: tc.members}).runs(DefaultThresholds(), at) {
				var ids []string
				for _, m := range run.members {
					ids = append(ids, m.riderID)
					placed[m.riderID], own[m.riderID] = m.weighedAt(), m.along
				}
				slices.Sort(ids)
				got = append(got, ids)
			}
			slices.SortFunc(got, func(x, y []string) int { return cmp.Compare(x[0], y[0]) })
			assert.Equal(t, tc.want, got)
			for id, want := range tc.placed {
				assert.InDelta(t, want, placed[id], 1, "where %s is handed to the estimate", id)
			}
			for id, want := range tc.own {
				assert.InDelta(t, want, own[id], 1, "where %s's own point puts them", id)
			}
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

// A run joined by a rider whose point is stale is published where that
// rider's carry puts them, not where their old point stands: Y's point is
// 35 s old at 300 m and 15 m/s, so Y is with Z, stopped at 800 m, and both
// count towards Z's vehicle rather than trimming each other out of it.
func TestAggregator_StaleRiderCarriedOntoARunIsPublishedWithIt(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	f.addSession("y1", "rider-y1", TierTrusted)
	f.addSession("z1", "rider-z1", TierTrusted)
	var ys []Point
	for a := 250.0; a <= 300; a += 10 {
		p := f.onSchedulePoint(a, 265*time.Second)
		p.Speed = 15
		ys = append(ys, p)
	}
	_, err := f.agg.ApplyBatch("y1", ys, nil, ys[len(ys)-1].Timestamp.Add(2*time.Second))
	require.NoError(t, err)
	now := f.walkOffset(t, "z1", 700, 800, 10, 0)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, 2, est[0].Riders)
	assert.InDelta(t, 800, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 30)
}

// rideTo registers a trusted ride on T1 whose last point is at along at at,
// each of its points reporting speed; it reaches along over the 30 s before,
// from up to 50 m back.
func (f *aggFixture) rideTo(t *testing.T, id string, along, speed float64, at time.Time) {
	t.Helper()
	f.rideToAs(t, id, TierTrusted, along, speed, at)
}

// rideToAs is rideTo for a rider of the given tier.
func (f *aggFixture) rideToAs(t *testing.T, id string, tier Tier, along, speed float64, at time.Time) {
	t.Helper()
	f.addSession(id, "rider-"+id, tier)
	var pts []Point
	for i := 5; i >= 0; i-- {
		pts = append(pts, Point{Pos: f.t1.Shape.PointAt(max(0, along-float64(i)*10)), Accuracy: 5, Speed: speed,
			Timestamp: at.Add(-time.Duration(i) * 6 * time.Second)})
	}
	_, err := f.agg.ApplyBatch(id, pts, nil, at.Add(2*time.Second))
	require.NoError(t, err)
}

// A stale rider that joins a moving run is weighed against that run's rider
// at the same moment. F is 80 s old at 500 m and 1 m/s, so its run's vehicle
// is carried to 580 m; S is 50 s old at 0 m and 15 m/s, carried to 750 m and
// near enough to join. With F left at the 500 m it reported, both were 125 m
// from their median and trimmed, and F's vehicle was not published. The Y/Z
// test does not see this: Z is stopped, so its point is already where it is.
func TestAggregator_StaleRiderJoiningAMovingRunKeepsItsRider(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	now := f.base.Add(10 * time.Minute)
	f.rideTo(t, "F", 500, 1, now.Add(-80*time.Second))
	f.rideTo(t, "S", 0, 15, now.Add(-50*time.Second))
	f.rideTo(t, "N", 1000, 1.7, now)

	est := f.agg.Estimates(now.Add(2*time.Second), noCover)
	var at []float64
	for _, e := range est {
		at = append(at, f.t1.Shape.Project(e.Pos, nil).AlongShape)
	}
	slices.Sort(at)
	require.Len(t, at, 2, "F's run and N's are both published: %v", at)
	assert.InDelta(t, 665, at[0], 30)
	assert.InDelta(t, 1000, at[1], 30)
}

// Whether a run may be published is decided on where its riders' own points
// put them, not on where a stale rider is placed for the estimate: a rider
// placed where another rider's point is carried always agrees with that
// rider, and vouches for nothing. No rider here is trusted, so a run is
// published only if two of its riders vouch for each other.
func TestAggregator_PlacedRiderVouchesOnlyFromItsOwnPoint(t *testing.T) {
	type ride struct {
		id           string
		along, speed float64
		age          time.Duration
	}
	tests := []struct {
		name      string
		rides     []ride
		published bool
		// at is where along the shape the run is published.
		at float64
	}{
		{"a stale rider standing 160 m from the run's rider does not vouch for it",
			[]ride{{"F", 500, 0, 0}, {"S", 340, 15, 30 * time.Second}}, false, 0},
		{"a stale rider standing 60 m from the run's rider vouches for it and is weighed at its vehicle",
			[]ride{{"F", 500, 0, 0}, {"S", 440, 15, 30 * time.Second}}, true, 500},
		{"a stale group placed at one spot does not vouch for itself",
			[]ride{{"F", 720, 0, 0}, {"T1", 100, 10, 50 * time.Second}, {"T2", 250, 10, 60 * time.Second}}, false, 0},
		{"a stale point carried onto the run's rider vouches for it",
			[]ride{{"F", 600, 0, 0}, {"T1", 100, 10, 50 * time.Second}}, true, 600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAggFixture(t)
			headwayT1(f)
			now := f.base.Add(10 * time.Minute)
			for _, r := range tc.rides {
				f.rideToAs(t, r.id, TierNew, r.along, r.speed, now.Add(-r.age))
			}
			now = now.Add(2 * time.Second)

			est := f.agg.Estimates(now, noCover)
			reported, riders := f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
			require.Equal(t, len(tc.rides), riders, "every rider counts towards the trip")
			assert.Equal(t, tc.published, reported)
			if !tc.published {
				assert.Empty(t, est)
				return
			}
			require.Len(t, est, 1)
			assert.Equal(t, len(tc.rides), est[0].Riders)
			assert.InDelta(t, tc.at, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 10)
		})
	}
}

// A run is stamped with when its place is true. One that takes in a stale
// rider is weighed as of the newest point in the trip, though that point is
// another run's, and is stamped then; one of only its own riders is weighed
// as they reported and stamped with its own newest point. F is 80 s old at
// 500 m and 1 m/s, S 50 s old at 0 m and 15 m/s, N new at 1000 m. Ages are
// back from N's point, and want is each run's stamp in that reckoning, the
// run behind first.
func TestAggregator_RunIsStampedWhenItsPlaceIsTrue(t *testing.T) {
	type ride struct {
		id           string
		along, speed float64
		age          time.Duration
	}
	f, s, n := ride{"F", 500, 1, 80 * time.Second}, ride{"S", 0, 15, 50 * time.Second}, ride{"N", 1000, 1.7, 0}
	tests := []struct {
		name  string
		rides []ride
		// now is when the estimates are made, after N's point.
		now  time.Duration
		want []time.Duration
	}{
		{"a run of only its own riders keeps its newest point's time",
			[]ride{f, n}, 2 * time.Second, []time.Duration{80 * time.Second, 0}},
		{"a run that takes in a stale rider is stamped at N's moment",
			[]ride{f, s, n}, 2 * time.Second, []time.Duration{0, 0}},
		{"a clock running ahead stamps both runs no later than now",
			[]ride{f, s, n}, -10 * time.Second, []time.Duration{10 * time.Second, 10 * time.Second}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newAggFixture(t)
			headwayT1(fx)
			at := fx.base.Add(10 * time.Minute)
			for _, r := range tc.rides {
				fx.rideTo(t, r.id, r.along, r.speed, at.Add(-r.age))
			}

			est := fx.agg.Estimates(at.Add(tc.now), noCover)
			slices.SortFunc(est, func(x, y TripEstimate) int {
				return cmp.Compare(fx.t1.Shape.Project(x.Pos, nil).AlongShape, fx.t1.Shape.Project(y.Pos, nil).AlongShape)
			})
			require.Len(t, est, len(tc.want))
			for i, age := range tc.want {
				assert.Equal(t, at.Add(-age), est[i].Timestamp, "run %d", i)
			}
		})
	}
}

// A kept run's start does not move when the run takes in a stale rider on
// one tick and not the next. F is 60 s old at 500 m and 1.5 m/s; S, older
// still at 85 s, is at 50 m and 8.4 m/s, carried to 764 m, and joins F's run.
// Weighed at N's moment, F's run is 177 m on from where F's own point puts it
// 60 s before: as far as a vehicle goes in 60 s, so each tick finds the run
// it kept. Stamped with F's point either way, it moved 177 m in no time,
// further than any vehicle could, and took a start of its own.
func TestAggregator_RunStartStaysAsAStaleRiderComesAndGoes(t *testing.T) {
	for _, staleFirst := range []bool{true, false} {
		name := "stale rider on the second tick"
		if staleFirst {
			name = "stale rider on the first tick"
		}
		t.Run(name, func(t *testing.T) {
			f := newAggFixture(t)
			headwayT1(f)
			at := f.base.Add(10 * time.Minute)
			f.rideTo(t, "F", 500, 1.5, at.Add(-60*time.Second))
			f.rideTo(t, "N", 1000, 1.7, at)
			if staleFirst {
				f.rideTo(t, "S", 50, 8.4, at.Add(-85*time.Second))
			}
			now := at.Add(2 * time.Second)
			first := estimateStarts(t, f, now)
			require.Len(t, first, 2)

			if staleFirst {
				_, ok := f.agg.End("S", EndArrived, now)
				require.True(t, ok)
			} else {
				f.rideTo(t, "S", 50, 8.4, at.Add(-85*time.Second))
			}
			now = now.Add(time.Second)
			est := f.agg.Estimates(now, noCover)
			require.Len(t, est, 2)
			assert.Equal(t, first, estimateStarts(t, f, now))
		})
	}
}

// Two runs at one start_time with as many riders each: the tie goes to the
// run with the newer point, not to the one stamped later only because it was
// weighed at a third run's newest point. Q has just reported at 30 m, on a
// run of its own that left at 08:10. M and N, 5 s ago at 550 m and 570 m, are
// a run that left at 08:00. So is F's, ahead at 850 m: S's stale point puts S
// on it, so it is weighed and stamped at Q's moment, though its newest point,
// S's, is 30 s old.
func TestAggregator_RunsAtOneStartTimeTieGoesToTheNewerPointNotTheStamp(t *testing.T) {
	f := newAggFixture(t)
	f.t1.frequencies = []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased}}
	at := f.base.Add(10 * time.Minute)
	f.rideTo(t, "Q", 30, 1.7, at)
	f.rideTo(t, "M", 550, 1.7, at.Add(-10*time.Second))
	f.rideTo(t, "N", 570, 1.7, at.Add(-5*time.Second))
	f.rideTo(t, "F", 850, 1, at.Add(-80*time.Second))
	f.rideTo(t, "S", 750, 10, at.Add(-30*time.Second))

	est := f.agg.Estimates(at.Add(2*time.Second), noCover)
	require.Len(t, est, 2)
	assert.Equal(t, "08:00:00", est[0].StartTime)
	assert.Equal(t, 2, est[0].Riders)
	assert.InDelta(t, 560, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 20)
	assert.Equal(t, at.Add(-5*time.Second), est[0].Timestamp)
	assert.Equal(t, "08:10:00", est[1].StartTime)
}

// A run stamped later than its own riders' points can be wrong about where
// its vehicle was then: F reported at a stop 85 s before N's point, S's
// stale point puts S on F's run, and the run is stamped at N's moment with F
// still at the stop. The vehicle has since left: F's next report, 5 s after
// that stamp, is 400 m on. That is further than a vehicle goes in 5 s but not
// in the 35 s since S's point, the newest of the run's own, so the run keeps
// its start.
func TestAggregator_RunStartStaysWhenItsStampOutrunsItsRiders(t *testing.T) {
	f := newAggFixture(t)
	headwayT1(f)
	at := f.base.Add(10 * time.Minute)
	f.rideTo(t, "F", 100, 0, at.Add(-85*time.Second))
	f.rideTo(t, "S", 50, 10, at.Add(-30*time.Second))
	f.rideTo(t, "N", 900, 1.7, at)
	now := at.Add(2 * time.Second)
	first := estimateStarts(t, f, now)
	require.Len(t, first, 2)

	_, ok := f.agg.End("S", EndArrived, now)
	require.True(t, ok)
	moved := Point{Pos: f.t1.Shape.PointAt(500), Accuracy: 5, Speed: 8, Timestamp: at.Add(5 * time.Second)}
	_, err := f.agg.ApplyBatch("F", []Point{moved}, nil, moved.Timestamp.Add(2*time.Second))
	require.NoError(t, err)
	now = moved.Timestamp.Add(2 * time.Second)
	est := f.agg.Estimates(now, noCover)
	require.True(t, slices.ContainsFunc(est, func(e TripEstimate) bool {
		return math.Abs(f.t1.Shape.Project(e.Pos, nil).AlongShape-500) < 10
	}), "F's run is published where F now is")
	assert.Equal(t, first, estimateStarts(t, f, now))
}

// Two runs at one start_time with as many riders each, both reported by
// phones whose clocks run ahead: the tie still goes to the newer point. A's
// point, at 600 m, is 10 s ahead of now and B's, at 900 m, 20 s ahead; both
// runs are stamped now, and B's is the run published.
func TestAggregator_RunsAtOneStartTimeTieReadsClocksAheadAsReported(t *testing.T) {
	f := newAggFixture(t)
	f.t1.frequencies = []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(22, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased}}
	now := f.base.Add(10 * time.Minute)
	f.rideTo(t, "A", 600, 1.7, now.Add(10*time.Second))
	f.rideTo(t, "B", 900, 1.7, now.Add(20*time.Second))

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, "08:00:00", est[0].StartTime)
	assert.InDelta(t, 900, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 10)
	assert.Equal(t, now, est[0].Timestamp)
}
