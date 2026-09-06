package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aggFixture returns an aggregator plus helpers to build on-schedule points
// for T1 on 20260902 starting at 08:00 local.
type aggFixture struct {
	ix   *Index
	agg  *Aggregator
	t1   *TripInfo
	base time.Time // 08:00 local
}

func newAggFixture(t *testing.T) *aggFixture {
	t.Helper()
	ix := fixtureIndex(t)
	t1, _ := ix.Trip("T1")
	return &aggFixture{
		ix: ix, agg: NewAggregator(DefaultThresholds(), ix.Timezone()), t1: t1,
		base: time.Date(2026, 9, 2, 8, 0, 0, 0, ix.Timezone()),
	}
}

// onSchedulePoint is at `along` metres at the scheduled time for that spot
// (T1 runs 1001 m in 10 minutes ≈ 1.67 m/s) plus `offset`.
func (f *aggFixture) onSchedulePoint(along float64, offset time.Duration) Point {
	ts := f.base.Add(time.Duration(along/1001*600) * time.Second).Add(offset)
	return Point{Pos: f.t1.Shape.PointAt(along), Accuracy: 5, Speed: 1.7, Bearing: 0, Timestamp: ts}
}

func (f *aggFixture) addSession(id, riderID string, tier Tier) *Session {
	s := NewSession(id, riderID, TripKey{"T1", "20260902"}, f.t1, tier, f.base)
	f.agg.Add(s)
	return s
}

func (f *aggFixture) walk(t *testing.T, rideID string, from, to, step float64, lookup func(TripKey) (TrustedVehicle, bool)) BatchResult {
	t.Helper()
	var pts []Point
	for a := from; a <= to; a += step {
		pts = append(pts, f.onSchedulePoint(a, 0))
	}
	now := pts[len(pts)-1].Timestamp.Add(2 * time.Second)
	res, err := f.agg.ApplyBatch(rideID, pts, lookup, now)
	require.NoError(t, err)
	return res
}

func noCover(TripKey) bool { return false }

func TestAggregator_ApplyBatch_VerifiesAndReportsResult(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	res := f.walk(t, "r1", 0, 60, 10, nil) // 7 points, 6 s apart... all matched
	assert.Equal(t, Verified, res.Summary.State)
	assert.Equal(t, 7, res.Accepted)
	assert.Equal(t, 0, res.Ignored)
	assert.False(t, res.Published, "new rider without corroboration")
	assert.Equal(t, Unavailable, res.Corroboration)
	assert.Len(t, res.Points, 7)
	assert.Equal(t, Matched, res.Points[0].Verdict.Outcome)
	assert.False(t, res.Summary.Ended())

	snap, ok := f.agg.Snapshot("r1")
	assert.True(t, ok)
	assert.Equal(t, "rider-a", snap.RiderID)
	assert.False(t, snap.Ended)
	id, ok := f.agg.ActiveRideForRider("rider-a")
	assert.True(t, ok)
	assert.Equal(t, "r1", id)
	assert.Equal(t, 1, f.agg.ActiveCount())
}

func TestAggregator_ApplyBatch_UnknownRide(t *testing.T) {
	f := newAggFixture(t)
	_, err := f.agg.ApplyBatch("nope", []Point{f.onSchedulePoint(0, 0)}, nil, f.base)
	assert.ErrorIs(t, err, ErrUnknownRide)
}

func TestAggregator_ApplyBatch_SortsAndStopsAtEnd(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	// Reversed order + 6 off-route points: 5 reject, the 6th and the trailing on-route point are ignored.
	off := LatLon{47.6045, -122.3200} // ~750 m east of the line
	pts := []Point{
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(50 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(40 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(30 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(20 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(10 * time.Second)},
		{Pos: off, Accuracy: 5, Timestamp: f.base.Add(60 * time.Second)},
		f.onSchedulePoint(100, 0),
	}
	res, err := f.agg.ApplyBatch("r1", pts, nil, f.base.Add(70*time.Second))
	require.NoError(t, err)
	assert.True(t, res.Summary.Ended())
	assert.Equal(t, EndOffRoute, res.Summary.EndReason)
	assert.Equal(t, Rejected, res.Summary.State)
	assert.Equal(t, 5, res.Accepted)
	assert.Equal(t, 2, res.Ignored)
	snap, ok := f.agg.Snapshot("r1")
	require.True(t, ok, "an ended ride stays registered until it is filed")
	assert.True(t, snap.Ended, "the session records that it ended")
	_, err = f.agg.ApplyBatch("r1", pts[:1], nil, f.base)
	assert.ErrorIs(t, err, ErrUnknownRide)
	assert.NotNil(t, f.agg.Remove("r1"))
	assert.Nil(t, f.agg.Remove("r1"))
}

// offRoutePoint is ~750 m east of T1's line, so it can never match the shape.
func (f *aggFixture) offRoutePoint(offset time.Duration) Point {
	return Point{Pos: LatLon{47.6045, -122.3200}, Accuracy: 5, Speed: 1.7, Timestamp: f.base.Add(offset)}
}

func TestAggregator_ApplyBatch_AppliesOldestFirst(t *testing.T) {
	f := newAggFixture(t)
	s := f.addSession("r1", "rider-a", TierNew)
	// Newest first. Verify refuses a point that is not newer than the previous
	// one, so without sorting only the first of these would ever be applied.
	var pts []Point
	for a := 30.0; a >= 0; a -= 10 {
		pts = append(pts, f.onSchedulePoint(a, 0))
	}
	res, err := f.agg.ApplyBatch("r1", pts, nil, f.onSchedulePoint(30, 2*time.Second).Timestamp)
	require.NoError(t, err)
	assert.Equal(t, 4, res.Accepted)
	assert.Equal(t, 0, res.Ignored)
	assert.Equal(t, Verified, res.Summary.State)
	for _, ap := range res.Points {
		assert.Equal(t, Matched, ap.Verdict.Outcome)
	}
	require.NotNil(t, s.Latest())
	assert.InDelta(t, 30, s.Latest().AlongShape, 2, "the newest point is applied last")
}

func TestAggregator_TrustedRiderPublishes(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	res := f.walk(t, "r1", 0, 30, 10, nil)
	assert.True(t, res.Published)
	now := f.onSchedulePoint(30, 5*time.Second).Timestamp
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, TripKey{"T1", "20260902"}, est[0].Key)
	assert.Equal(t, "R1", est[0].RouteID)
	assert.Equal(t, 1, est[0].Riders)
	assert.InDelta(t, 30, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 2)
	assert.InDelta(t, 0, est[0].Bearing, 1)
	assert.Equal(t, "ST2", est[0].StopID)
	assert.Equal(t, 2, est[0].StopSequence)
	require.NotNil(t, est[0].Speed)
	assert.InDelta(t, 1.7, *est[0].Speed, 0.01)
	assert.False(t, est[0].Timestamp.After(now))
	assert.Equal(t, 1, f.agg.PublishableCount(now, noCover))
}

func TestAggregator_CorroboratedNewRiderPublishes(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	lookup := func(k TripKey) (TrustedVehicle, bool) {
		return TrustedVehicle{VehicleID: "v", Pos: f.t1.Shape.PointAt(80), Timestamp: f.base.Add(50 * time.Second)}, true
	}
	res := f.walk(t, "r1", 0, 110, 10, lookup) // 12 points, each within 150 m + age allowance
	assert.True(t, res.Summary.Corroborated)
	assert.True(t, res.Published)
	assert.Equal(t, Corroborated, res.Corroboration)
}

func TestAggregator_ConsensusPublishes(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	f.addSession("r2", "rider-b", TierNew)
	f.walk(t, "r1", 0, 30, 10, nil)
	f.walk(t, "r2", 20, 50, 10, nil)
	now := f.onSchedulePoint(50, 5*time.Second).Timestamp
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1, "two new riders within 100 m reach consensus")
	assert.Equal(t, 2, est[0].Riders)
	assert.InDelta(t, 40, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 3, "median of 30 and 50")

	reported, riders := f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.True(t, reported)
	assert.Equal(t, 2, riders)

	// A blocked rider contributes nothing, so the trip is down to one rider.
	f.agg.SetTier("rider-b", TierBlocked)
	_, riders = f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.Equal(t, 1, riders)
}

func TestAggregator_NoConsensusWhenSameRiderOrFarApart(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	f.addSession("r2", "rider-a", TierNew) // same rider id → r1 is replaced as active ride but both sessions exist
	f.walk(t, "r1", 0, 30, 10, nil)
	f.walk(t, "r2", 20, 50, 10, nil)
	now := f.onSchedulePoint(50, 5*time.Second).Timestamp
	assert.Empty(t, f.agg.Estimates(now, noCover), "same rider twice is not consensus")

	g := newAggFixture(t)
	g.addSession("r1", "rider-a", TierNew)
	g.addSession("r2", "rider-b", TierNew)
	g.walk(t, "r1", 0, 30, 10, nil)
	g.walk(t, "r2", 300, 330, 10, nil)
	now = g.onSchedulePoint(330, 5*time.Second).Timestamp
	assert.Empty(t, g.agg.Estimates(now, noCover), "300 m apart is not consensus")
	reported, riders := g.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.False(t, reported)
	// Only r2 still counts: by 08:03:22 r1's last point is over three minutes
	// old, and a stale rider is no longer saying where the vehicle is.
	assert.Equal(t, 1, riders)
}

func TestAggregator_OutlierExcludedFromEstimate(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	f.addSession("r2", "rider-b", TierTrusted)
	f.addSession("r3", "rider-c", TierTrusted)
	f.walk(t, "r1", 0, 100, 10, nil)
	f.walk(t, "r2", 10, 110, 10, nil)
	// r3 is 400 m ahead but reports at the same wall-clock times as r1, so it
	// stays fresh (3 min early is inside the schedule window).
	var pts []Point
	for i, a := 0, 400.0; a <= 500; a, i = a+10, i+1 {
		pts = append(pts, Point{Pos: f.t1.Shape.PointAt(a), Accuracy: 5, Speed: 1.7, Timestamp: f.base.Add(time.Duration(i*6) * time.Second)})
	}
	_, err := f.agg.ApplyBatch("r3", pts, nil, f.base.Add(70*time.Second))
	require.NoError(t, err)
	now := f.base.Add(70 * time.Second)
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, 2, est[0].Riders)
	assert.InDelta(t, 105, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 3)
}

func TestAggregator_EstimateUsesLatestMatchedPoint(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	f.walk(t, "r1", 0, 100, 10, nil)
	// Three off-route points: accepted and freshening the ride, but short of the
	// five that would reject it, so the rider is still verified and publishing.
	pts := []Point{f.offRoutePoint(65 * time.Second), f.offRoutePoint(70 * time.Second), f.offRoutePoint(75 * time.Second)}
	now := f.base.Add(80 * time.Second)
	res, err := f.agg.ApplyBatch("r1", pts, nil, now)
	require.NoError(t, err)
	require.Equal(t, Verified, res.Summary.State)
	require.False(t, res.Summary.Ended())
	require.True(t, res.Published)

	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.InDelta(t, 100, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 3,
		"the vehicle is where the rider last matched, not where they wandered off to")
	assert.Equal(t, f.onSchedulePoint(100, 0).Timestamp, est[0].Timestamp,
		"the estimate is dated by the point that positioned it")
	assert.Equal(t, 1, est[0].Riders)
}

func TestAggregator_CoveredAndStaleAndBlocked(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	f.walk(t, "r1", 0, 30, 10, nil)
	now := f.onSchedulePoint(30, 5*time.Second).Timestamp
	assert.Empty(t, f.agg.Estimates(now, func(TripKey) bool { return true }), "trusted feed covers the trip")
	assert.Empty(t, f.agg.Estimates(now.Add(2*time.Minute), noCover), "stale after 90 s")
	f.agg.SetTier("rider-a", TierBlocked)
	assert.Empty(t, f.agg.Estimates(now, noCover))
	f.agg.SetTier("rider-a", TierTrusted)
	assert.Len(t, f.agg.Estimates(now, noCover), 1)
}

func TestAggregator_Reap(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("idle", "rider-a", TierNew)
	f.addSession("old", "rider-b", TierNew)
	f.addSession("live", "rider-c", TierNew)
	f.walk(t, "live", 0, 10, 10, nil)
	_, err := f.agg.ApplyBatch("idle", []Point{f.onSchedulePoint(0, 0)}, nil, f.base.Add(5*time.Second))
	require.NoError(t, err)

	// At 08:16 every session's last accepted point (or start, for "old") is
	// more than 15 minutes old, so all three are reaped as idle.
	reaped := f.agg.Reap(f.base.Add(16 * time.Minute))
	assert.Equal(t, []string{"idle", "live", "old"}, reaped)
	for _, id := range reaped {
		snap, ok := f.agg.Snapshot(id)
		require.True(t, ok, "a reaped ride stays registered until it is filed")
		assert.True(t, snap.Ended, id)
		assert.Equal(t, EndIdle, snap.Summary.EndReason, id)
	}
	assert.Equal(t, 0, f.agg.ActiveCount(), "an ended ride is not an active one")
	_, ok := f.agg.ActiveRideForRider("rider-a")
	assert.False(t, ok, "an ended ride is not the rider's active ride")

	// Reaping again returns the same ids: their outcomes are still unfiled.
	assert.Equal(t, reaped, f.agg.Reap(f.base.Add(17*time.Minute)))

	g := newAggFixture(t)
	g.addSession("old", "rider-b", TierNew)
	_, err = g.agg.ApplyBatch("old", []Point{g.onSchedulePoint(0, 0)}, nil, g.base.Add(5*time.Second))
	require.NoError(t, err)
	// Keep it non-idle but past 3 h: the max-duration rule wins.
	reaped = g.agg.Reap(g.base.Add(3*time.Hour + time.Second))
	require.Equal(t, []string{"old"}, reaped)
	snap, ok := g.agg.Snapshot("old")
	require.True(t, ok)
	assert.Equal(t, EndMaxDuration, snap.Summary.EndReason)
	assert.Equal(t, 0, g.agg.ActiveCount())
}

// walkOffset is walk with every timestamp shifted, so riders at different
// points of the shape can report at the same moment.
func (f *aggFixture) walkOffset(t *testing.T, rideID string, from, to, step float64, offset time.Duration) time.Time {
	t.Helper()
	var pts []Point
	for a := from; a <= to; a += step {
		pts = append(pts, f.onSchedulePoint(a, offset))
	}
	now := pts[len(pts)-1].Timestamp.Add(2 * time.Second)
	_, err := f.agg.ApplyBatch(rideID, pts, nil, now)
	require.NoError(t, err)
	return now
}

// TestAggregator_TrimmedGroupMustStillBePublishable: a trusted rider makes the
// group publishable, but the crowd's median puts them outside the trim. What is
// left — new riders too far apart to agree — must not be published on the
// trusted rider's credit.
func TestAggregator_TrimmedGroupMustStillBePublishable(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("trusted", "rider-t", TierTrusted)
	f.addSession("a", "rider-a", TierNew)
	f.addSession("b", "rider-b", TierNew)
	f.addSession("c", "rider-c", TierNew)
	// Everyone reports around the same moment (about 10 minutes in).
	f.walkOffset(t, "trusted", 40, 100, 10, 540*time.Second)
	f.walkOffset(t, "a", 640, 700, 10, 180*time.Second)
	f.walkOffset(t, "b", 790, 850, 10, 90*time.Second)
	now := f.walkOffset(t, "c", 940, 1000, 10, 0)

	assert.Empty(t, f.agg.Estimates(now, noCover), "the survivors of the trim are not credible on their own")
	assert.Equal(t, 0, f.agg.PublishableCount(now, noCover))
	reported, riders := f.agg.TripStatus(TripKey{"T1", "20260902"}, now, noCover)
	assert.False(t, reported)
	assert.Equal(t, 4, riders)
}

// TestAggregator_StalePointBehindNewerOffRoutePointsIsIgnored: a retried
// upload carrying an on-route point older than off-route points already
// applied must not be folded in — it would rewind the session and wipe the
// streak those points had built.
func TestAggregator_StalePointBehindNewerOffRoutePointsIsIgnored(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew)
	f.walk(t, "r1", 0, 60, 10, nil) // matched up to along 60

	offRoute := func(along float64) Point {
		p := f.onSchedulePoint(along, 0)
		p.Pos.Lon += 0.02 // ~1.5 km east of the shape
		return p
	}
	later := f.onSchedulePoint(90, 0).Timestamp.Add(2 * time.Second)
	res, err := f.agg.ApplyBatch("r1", []Point{offRoute(80), offRoute(90)}, nil, later)
	require.NoError(t, err)
	require.Equal(t, 2, res.OffRouteStreak)

	res, err = f.agg.ApplyBatch("r1", []Point{f.onSchedulePoint(65, 0)}, nil, later)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Accepted)
	assert.Equal(t, 1, res.Ignored)
	assert.Equal(t, 2, res.OffRouteStreak, "the streak stands")

	res, err = f.agg.ApplyBatch("r1", []Point{offRoute(100)}, nil, later.Add(10*time.Second))
	require.NoError(t, err)
	assert.Equal(t, 3, res.OffRouteStreak, "had the stale match been applied, the streak would have restarted at 1")
}

// A rider's superseded ride stays registered so its points can still be reaped,
// but it must not contribute a second along value to its trip's estimate: two
// entries from one phone weight the median twice and survive outlier trimming
// as a pair, which distinctRiders hides from the rider count but not from the
// published position.
func TestAggregator_SupersededSessionDoesNotShiftEstimate(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierNew) // superseded below
	f.addSession("r2", "rider-b", TierNew)
	f.addSession("r3", "rider-a", TierNew) // rider-a's newest ride
	f.walk(t, "r1", 0, 100, 10, nil)
	f.walk(t, "r3", 0, 100, 10, nil)
	f.walk(t, "r2", 100, 180, 10, nil)

	now := f.base.Add(125 * time.Second)
	est := f.agg.Estimates(now, noCover)
	require.Len(t, est, 1)
	assert.Equal(t, 2, est[0].Riders)
	// rider-a at 100 and rider-b at 180 median to 140. Counting the superseded
	// r1 as a third member would drag the median back onto 100.
	assert.InDelta(t, 140, f.t1.Shape.Project(est[0].Pos, nil).AlongShape, 3)
}

// Two riders far enough apart that the median lands between them leaves the
// outlier trim with nobody, and the midpoint is a place neither rider is.
func TestAggregator_Estimates_SplitGroupPublishesNothing(t *testing.T) {
	f := newAggFixture(t)
	f.addSession("r1", "rider-a", TierTrusted)
	f.addSession("r2", "rider-b", TierNew)

	// Both riders report at the same wall-clock moment, 440 m apart along the
	// shape: rider-b is running early, which the schedule window allows.
	apply := func(rideID string, from, to float64, offset time.Duration) {
		t.Helper()
		var pts []Point
		for a := from; a <= to; a += 10 {
			pts = append(pts, f.onSchedulePoint(a, offset))
		}
		res, err := f.agg.ApplyBatch(rideID, pts, nil, f.base.Add(40*time.Second))
		require.NoError(t, err)
		require.Equal(t, Verified, res.Summary.State)
	}
	apply("r1", 0, 60, 0)
	apply("r2", 440, 500, -270*time.Second)

	assert.Empty(t, f.agg.Estimates(f.base.Add(40*time.Second), noCover),
		"a group with no surviving members has no position to publish")
}
