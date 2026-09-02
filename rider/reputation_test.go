package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestScoreDelta(t *testing.T) {
	cases := []struct {
		name string
		sum  RideSummary
		want int
	}{
		{"corroborated 5 min", RideSummary{State: Verified, EndReason: EndArrived, Corroborated: true, MatchedDuration: 5 * time.Minute}, 1},
		{"corroborated but short", RideSummary{State: Verified, EndReason: EndArrived, Corroborated: true, MatchedDuration: 4 * time.Minute}, 0},
		{"verified uncorroborated", RideSummary{State: Verified, EndReason: EndUserRequested, MatchedDuration: 20 * time.Minute}, 0},
		{"ended while pending", RideSummary{State: Pending, EndReason: EndSuperseded}, 0},
		{"rejected off route", RideSummary{State: Rejected, EndReason: EndOffRoute}, -1},
		{"rejected implausible", RideSummary{State: Rejected, EndReason: EndImplausible}, -1},
		{"rejected off schedule", RideSummary{State: Rejected, EndReason: EndOffSchedule}, -1},
		{"rejected contradicted", RideSummary{State: Rejected, EndReason: EndContradicted}, -3},
		{"corroborated then superseded", RideSummary{State: Verified, EndReason: EndSuperseded, Corroborated: true, MatchedDuration: 6 * time.Minute}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, ScoreDelta(tc.sum)) })
	}
}

func TestTierForAndClamp(t *testing.T) {
	assert.Equal(t, TierBlocked, TierFor(-3))
	assert.Equal(t, TierNew, TierFor(-2))
	assert.Equal(t, TierNew, TierFor(2))
	assert.Equal(t, TierTrusted, TierFor(3))
	assert.Equal(t, 10, Clamp(11))
	assert.Equal(t, -10, Clamp(-11))
	assert.Equal(t, 4, Clamp(4))
	assert.Equal(t, TierNew, ParseTier("weird"))
	assert.Equal(t, TierTrusted, ParseTier("trusted"))
}
