package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSession(t *testing.T) *Session {
	t.Helper()
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T1")
	return NewSession("ride-1", "rider-1", TripKey{"T1", "20260902"}, trip, TierNew, time.Unix(1_756_800_000, 0))
}

func verdict(o Outcome, c Corroboration, along float64) Verdict {
	return Verdict{Outcome: o, Corroboration: c, AlongShape: along}
}

func pointAt(sec int) Point {
	return Point{Timestamp: time.Unix(1_756_800_000+int64(sec), 0), Speed: 8}
}

func TestSession_PendingToVerified(t *testing.T) {
	s := newTestSession(t)
	assert.Equal(t, Pending, s.State())
	assert.Nil(t, s.Latest())
	var tr Transition
	for i := 1; i <= 3; i++ {
		tr = s.Apply(verdict(Matched, Unavailable, float64(i*10)), pointAt(i*5))
	}
	assert.True(t, tr.StateChanged)
	assert.Equal(t, Verified, s.State())
	assert.Equal(t, 30.0, s.Latest().AlongShape)
	assert.Equal(t, Counts{Total: 3, Matched: 3}, s.Counts())
	assert.Equal(t, pointAt(15).Timestamp, s.LastAcceptedAt())
}

func TestSession_IgnoredTouchesNothing(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(5))
	s.Apply(verdict(Ignored, Unavailable, 0), pointAt(6))
	assert.Equal(t, Counts{Total: 1, Matched: 1}, s.Counts())
	assert.Equal(t, 10.0, s.Latest().AlongShape)
}

func TestSession_RejectAfterFiveNonMatched_MostFrequentReason(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(5))
	outs := []Outcome{OffRoute, Implausible, OffRoute, OffSchedule, OffRoute}
	var tr Transition
	for i, o := range outs {
		tr = s.Apply(verdict(o, Unavailable, 10), pointAt(10+i*5))
		if i < 4 {
			assert.False(t, tr.Ended)
			assert.Equal(t, i+1, s.OffRouteStreak())
		}
	}
	assert.True(t, tr.Ended)
	assert.Equal(t, EndOffRoute, tr.EndReason)
	assert.Equal(t, Rejected, s.State())
	assert.True(t, s.Ended())
}

func TestSession_MatchedResetsOffRouteStreak(t *testing.T) {
	s := newTestSession(t)
	for i := 0; i < 4; i++ {
		s.Apply(verdict(OffRoute, Unavailable, 0), pointAt(i*5))
	}
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(100))
	assert.Equal(t, 0, s.OffRouteStreak())
	tr := s.Apply(verdict(OffRoute, Unavailable, 0), pointAt(105))
	assert.False(t, tr.Ended)
}

func TestSession_ContradictedThreeTimesRejects(t *testing.T) {
	s := newTestSession(t)
	for i := 1; i <= 3; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(i)), pointAt(i*5))
	}
	require.Equal(t, Verified, s.State())
	s.Apply(verdict(Matched, Contradicted, 20), pointAt(20))
	s.Apply(verdict(Matched, Contradicted, 21), pointAt(25))
	s.Apply(verdict(Matched, Corroborated, 22), pointAt(30)) // resets
	s.Apply(verdict(Matched, Contradicted, 23), pointAt(35))
	s.Apply(verdict(Matched, Contradicted, 24), pointAt(40))
	tr := s.Apply(verdict(Matched, Contradicted, 25), pointAt(45))
	assert.True(t, tr.Ended)
	assert.Equal(t, EndContradicted, tr.EndReason)
	assert.Equal(t, Rejected, s.State())
	assert.Equal(t, 5, s.Counts().Contradicted)
	assert.Equal(t, 9, s.Counts().Matched, "contradicted points still count as matched geometry")
}

func TestSession_ContradictedCountsTowardVerification(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Contradicted, 1), pointAt(5))
	s.Apply(verdict(Matched, NoCorroboration, 2), pointAt(10))
	tr := s.Apply(verdict(Matched, Unavailable, 3), pointAt(15))
	assert.True(t, tr.StateChanged)
	assert.Equal(t, Verified, s.State())
}

func TestSession_CorroboratedAfterTwelve(t *testing.T) {
	s := newTestSession(t)
	for i := 1; i <= 11; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(i)), pointAt(i*5))
		assert.False(t, s.Corroborated())
	}
	s.Apply(verdict(Matched, Corroborated, 12), pointAt(60))
	assert.True(t, s.Corroborated())
	assert.Equal(t, Corroborated, s.LatestCorroboration())
	s.Apply(verdict(Matched, NoCorroboration, 13), pointAt(65))
	assert.True(t, s.Corroborated(), "sticky")
	assert.Equal(t, NoCorroboration, s.LatestCorroboration())
}

func TestSession_ApplyAfterEndIsNoop(t *testing.T) {
	s := newTestSession(t)
	s.End(EndUserRequested, pointAt(100).Timestamp)
	tr := s.Apply(verdict(Matched, Unavailable, 1), pointAt(101))
	assert.False(t, tr.StateChanged)
	assert.True(t, tr.Ended)
	assert.Equal(t, EndUserRequested, tr.EndReason)
	assert.Equal(t, Counts{}, s.Counts())
}

func TestSession_FreshAndPublishable(t *testing.T) {
	s := newTestSession(t)
	now := pointAt(200).Timestamp
	assert.False(t, s.Fresh(now, 90*time.Second))
	for i := 1; i <= 3; i++ {
		s.Apply(verdict(Matched, Unavailable, float64(i)), pointAt(150+i*5))
	}
	assert.True(t, s.Fresh(now, 90*time.Second)) // latest at 165, now 200
	assert.False(t, s.Fresh(now, 30*time.Second))
	assert.False(t, s.Publishable(now, 90*time.Second), "new rider, not corroborated")
	s.SetTier(TierTrusted)
	assert.True(t, s.Publishable(now, 90*time.Second))
	s.SetTier(TierBlocked)
	assert.False(t, s.Publishable(now, 90*time.Second))
	s.SetTier(TierNew)
	for i := 0; i < 12; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(100+i)), pointAt(170+i))
	}
	assert.True(t, s.Publishable(now, 90*time.Second), "corroborated new rider")
	s.End(EndArrived, now)
	assert.False(t, s.Publishable(now, 90*time.Second))
}

func TestSession_Summary(t *testing.T) {
	s := newTestSession(t)
	for i := 0; i < 12; i++ {
		s.Apply(verdict(Matched, Corroborated, float64(i)), pointAt(i*30))
	}
	s.End(EndArrived, pointAt(400).Timestamp)
	sum := s.Summary()
	assert.Equal(t, Verified, sum.State)
	assert.Equal(t, EndArrived, sum.EndReason)
	assert.True(t, sum.Corroborated)
	assert.Equal(t, 330*time.Second, sum.MatchedDuration)
	assert.Equal(t, 400*time.Second, sum.Duration)
	assert.Equal(t, 12, sum.Counts.Corroborated)
}

func TestParseClientEndReason(t *testing.T) {
	r, ok := ParseClientEndReason("user_requested")
	assert.True(t, ok)
	assert.Equal(t, EndUserRequested, r)
	for _, bad := range []string{"off_route", "superseded", "idle", "", "nope"} {
		_, ok := ParseClientEndReason(bad)
		assert.False(t, ok, bad)
	}
}

func TestStateString(t *testing.T) {
	assert.Equal(t, "pending", Pending.String())
	assert.Equal(t, "verified", Verified.String())
	assert.Equal(t, "rejected", Rejected.String())
	assert.Equal(t, "unknown", State(9).String())
}

func TestSession_LatestMatchedSkipsNonMatchedPoints(t *testing.T) {
	s := newTestSession(t)
	assert.Nil(t, s.LatestMatched())

	s.Apply(verdict(Matched, Unavailable, 10), pointAt(5))
	s.Apply(verdict(OffRoute, Unavailable, 0), pointAt(10))
	assert.Equal(t, 0.0, s.Latest().AlongShape, "an off-route point is still accepted")
	assert.Equal(t, 10.0, s.LatestMatched().AlongShape, "but it must not become the baseline")

	s.Apply(verdict(Matched, Contradicted, 20), pointAt(15))
	assert.Equal(t, 20.0, s.LatestMatched().AlongShape, "contradicted geometry still matched")
}

func TestSession_EndAfterRejectionKeepsRejectionReason(t *testing.T) {
	s := newTestSession(t)
	for i := 1; i <= 3; i++ {
		s.Apply(verdict(Matched, Contradicted, float64(i)), pointAt(i*5))
	}
	require.Equal(t, Rejected, s.State())
	require.Equal(t, EndContradicted, s.EndReason())
	rejectedAt := s.EndedAt()

	s.End(EndArrived, pointAt(100).Timestamp)
	assert.Equal(t, EndContradicted, s.EndReason(), "a late client end cannot overwrite a rejection")
	assert.Equal(t, rejectedAt, s.EndedAt())
	assert.Equal(t, Rejected, s.State())
}

func TestDominantOutcome(t *testing.T) {
	cases := []struct {
		name   string
		streak []Outcome
		want   Outcome
	}{
		{"clear majority", []Outcome{OffRoute, Implausible, OffRoute, OffSchedule, OffRoute}, OffRoute},
		{"tie goes to the later of the tied", []Outcome{OffRoute, OffRoute, Implausible, Implausible, OffSchedule}, Implausible},
		{"mirror image", []Outcome{Implausible, Implausible, OffRoute, OffRoute, OffSchedule}, OffRoute},
		{"the most recent outcome does not win on recency alone", []Outcome{OffRoute, OffRoute, OffRoute, Implausible, OffSchedule}, OffRoute},
		{"unanimous", []Outcome{OffSchedule, OffSchedule, OffSchedule, OffSchedule, OffSchedule}, OffSchedule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, dominantOutcome(tc.streak)) })
	}
}

func TestSession_FreshJudgesLatestMatch(t *testing.T) {
	s := newTestSession(t)
	s.Apply(verdict(Matched, Unavailable, 10), pointAt(100))
	s.Apply(verdict(OffRoute, Unavailable, 0), pointAt(180))
	now := pointAt(200).Timestamp
	assert.Equal(t, pointAt(180).Timestamp, s.LastAcceptedAt())
	assert.False(t, s.Fresh(now, 90*time.Second), "the match is 100 s old; an off-route point does not refresh it")
	assert.True(t, s.Fresh(now, 120*time.Second))
}
