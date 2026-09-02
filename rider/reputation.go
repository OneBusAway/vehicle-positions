package rider

import "time"

// Tier is what a rider's score buys them: whether their positions are published
// on their own, and whether they are published at all.
type Tier string

const (
	// TierNew is a rider whose positions need corroboration to be published.
	TierNew Tier = "new"
	// TierTrusted is a rider whose positions are published on their own.
	TierTrusted Tier = "trusted"
	// TierBlocked is a rider whose positions are never published.
	TierBlocked Tier = "blocked"
)

// ParseTier converts a stored tier string; anything unrecognised is treated as
// a new rider, which is the least privileged tier that still allows riding.
func ParseTier(s string) Tier {
	switch Tier(s) {
	case TierTrusted:
		return TierTrusted
	case TierBlocked:
		return TierBlocked
	}
	return TierNew
}

const (
	// minScoredRide is how long a ride's matched points must span before the
	// ride is worth reputation.
	minScoredRide = 5 * time.Minute
	// contradictedPenalty is heavier than the others: a trusted feed placing
	// the trip elsewhere is the strongest evidence a rider was not on it.
	contradictedPenalty = -3
	rejectedPenalty     = -1
	corroboratedReward  = 1

	blockedAtOrBelow = -3
	trustedAtOrAbove = 3
	maxScore         = 10
	minScore         = -10
)

// ScoreDelta is what a finished ride does to its rider's score. Rides that are
// merely inconclusive — ended while pending, or verified with no trusted feed
// to confirm them — move nothing.
func ScoreDelta(s RideSummary) int {
	if s.State == Rejected {
		if s.EndReason == EndContradicted {
			return contradictedPenalty
		}
		return rejectedPenalty
	}
	if s.Corroborated && s.MatchedDuration >= minScoredRide {
		return corroboratedReward
	}
	return 0
}

// TierFor is the tier a score earns.
func TierFor(score int) Tier {
	switch {
	case score <= blockedAtOrBelow:
		return TierBlocked
	case score >= trustedAtOrAbove:
		return TierTrusted
	}
	return TierNew
}

// Clamp keeps a score within range, so neither a long good history nor a long
// bad one takes many rides to reverse.
func Clamp(score int) int {
	return min(max(score, minScore), maxScore)
}
