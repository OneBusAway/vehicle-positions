package rider

import "time"

// State is how much the engine believes a ride is really on its trip.
type State int

const (
	// Pending means the ride has not yet produced enough matched points.
	Pending State = iota
	// Verified means the ride matched the trip often enough to be believed.
	Verified
	// Rejected means the ride disagreed with the trip and was abandoned.
	Rejected
)

func (s State) String() string {
	switch s {
	case Pending:
		return "pending"
	case Verified:
		return "verified"
	case Rejected:
		return "rejected"
	}
	return "unknown"
}

// EndReason records why a ride stopped. The first eight are the reasons a
// client may report; the rest are decided by the server.
type EndReason string

const (
	EndUserRequested       EndReason = "user_requested"
	EndArrived             EndReason = "arrived"
	EndStationary          EndReason = "stationary"
	EndMaxDuration         EndReason = "max_duration"
	EndLocationUnavailable EndReason = "location_unavailable"
	EndAuthorizationDenied EndReason = "authorization_denied"
	EndNetworkFailure      EndReason = "network_failure"
	EndAppTerminated       EndReason = "app_terminated"

	EndOffRoute      EndReason = "off_route"
	EndContradicted  EndReason = "contradicted"
	EndImplausible   EndReason = "implausible"
	EndOffSchedule   EndReason = "off_schedule"
	EndSuperseded    EndReason = "superseded"
	EndServerRestart EndReason = "server_restart"
	EndIdle          EndReason = "idle"
)

// clientEndReasons are the only reasons a client is allowed to report; a rider
// cannot, for instance, claim its own ride was superseded or rejected.
var clientEndReasons = map[EndReason]bool{
	EndUserRequested:       true,
	EndArrived:             true,
	EndStationary:          true,
	EndMaxDuration:         true,
	EndLocationUnavailable: true,
	EndAuthorizationDenied: true,
	EndNetworkFailure:      true,
	EndAppTerminated:       true,
}

// ParseClientEndReason converts a client-supplied string into an EndReason,
// reporting whether the client was allowed to send it.
func ParseClientEndReason(s string) (EndReason, bool) {
	r := EndReason(s)
	return r, clientEndReasons[r]
}

// TripKey identifies the run of a trip on one service day.
type TripKey struct{ TripID, StartDate string }

// Counts tallies the accepted points of a ride. Total counts every accepted
// point; Matched only those whose geometry matched, of which Corroborated and
// Contradicted are the ones a trusted feed had an opinion about.
type Counts struct{ Total, Matched, Corroborated, Contradicted int }

// Transition is what one Apply did to the session.
type Transition struct {
	StateChanged, Ended bool
	EndReason           EndReason
}

const (
	verifyStreak     = 3  // consecutive matched points that verify a ride
	rejectStreak     = 5  // consecutive non-matched points that reject a ride
	contradictStreak = 3  // consecutive contradicted points that reject a ride
	corroborateCount = 12 // corroborated points that make a ride corroborated for good
)

// Session is the state machine for one ride: it folds a stream of verdicts into
// a belief about whether the rider is really on the trip they claim. It holds
// no locks; the aggregator owns a session and serialises access to it.
type Session struct {
	id        string
	riderID   string
	key       TripKey
	trip      *TripInfo
	tier      Tier
	startedAt time.Time

	state         State
	corroborated  bool
	latest        *AcceptedPoint
	latestMatched *AcceptedPoint
	latestCorrob  Corroboration
	counts        Counts

	matchStreak      int
	contradictions   int
	nonMatchedStreak []Outcome // the current run of non-matched outcomes, never longer than rejectStreak

	firstMatchedAt time.Time
	lastMatchedAt  time.Time

	ended     bool
	endReason EndReason
	endedAt   time.Time
}

// NewSession starts a pending ride on the given trip.
func NewSession(id, riderID string, key TripKey, trip *TripInfo, tier Tier, startedAt time.Time) *Session {
	return &Session{id: id, riderID: riderID, key: key, trip: trip, tier: tier, startedAt: startedAt}
}

func (s *Session) ID() string           { return s.id }
func (s *Session) RiderID() string      { return s.riderID }
func (s *Session) Key() TripKey         { return s.key }
func (s *Session) Trip() *TripInfo      { return s.trip }
func (s *Session) StartedAt() time.Time { return s.startedAt }

// Tier is the rider's reputation tier as of now; the aggregator refreshes it
// with SetTier when a ride ends and the score moves.
func (s *Session) Tier() Tier     { return s.tier }
func (s *Session) SetTier(t Tier) { s.tier = t }

func (s *Session) State() State                       { return s.state }
func (s *Session) Corroborated() bool                 { return s.corroborated }
func (s *Session) LatestCorroboration() Corroboration { return s.latestCorrob }
func (s *Session) Counts() Counts                     { return s.counts }
func (s *Session) Ended() bool                        { return s.ended }
func (s *Session) EndReason() EndReason               { return s.endReason }
func (s *Session) EndedAt() time.Time                 { return s.endedAt }

// Latest is the most recent accepted point, whatever its outcome, and nil
// before the first one. The session keeps using it, so callers must not mutate
// what it points at.
func (s *Session) Latest() *AcceptedPoint { return s.latest }

// LatestMatched is the most recent accepted point whose geometry matched the
// trip — a contradicted point counts, because the geometry still matched — and
// nil before the first one. This, not Latest, is the baseline Verify should be
// given as Prev: an off-route or implausible point is exactly the position the
// next point must not be judged against. Callers must not mutate what it points
// at.
func (s *Session) LatestMatched() *AcceptedPoint { return s.latestMatched }

// OffRouteStreak is the length of the current run of non-matched points. It
// counts implausible and off-schedule points too, not only off-route ones; the
// name is fixed by the engine's API.
func (s *Session) OffRouteStreak() int { return len(s.nonMatchedStreak) }

// LastAcceptedAt is the timestamp of the latest accepted point, zero if there
// is none yet.
func (s *Session) LastAcceptedAt() time.Time {
	if s.latest == nil {
		return time.Time{}
	}
	return s.latest.Timestamp
}

// Apply folds one verdict into the session. Ignored verdicts touch nothing, and
// every other verdict is an accepted point. Once the ride has ended, Apply does
// nothing and reports the end it already had.
func (s *Session) Apply(v Verdict, p Point) Transition {
	if s.ended {
		return Transition{Ended: true, EndReason: s.endReason}
	}
	if v.Outcome == Ignored {
		return Transition{}
	}

	s.counts.Total++
	s.latest = &AcceptedPoint{Point: p, AlongShape: v.AlongShape}
	s.latestCorrob = v.Corroboration

	if v.Outcome != Matched {
		return s.applyNonMatched(v.Outcome, p.Timestamp)
	}
	s.latestMatched = s.latest
	return s.applyMatched(v.Corroboration, p.Timestamp)
}

// applyMatched folds in a point whose geometry matched the trip. A contradicted
// point still counts towards verification: the geometry matched, only the
// trusted feed disagreed.
func (s *Session) applyMatched(c Corroboration, at time.Time) Transition {
	s.counts.Matched++
	if s.counts.Matched == 1 {
		s.firstMatchedAt = at
	}
	s.lastMatchedAt = at
	s.nonMatchedStreak = s.nonMatchedStreak[:0]
	s.matchStreak++

	switch c {
	case Corroborated:
		s.counts.Corroborated++
		s.contradictions = 0
		if s.counts.Corroborated >= corroborateCount {
			s.corroborated = true
		}
	case Contradicted:
		s.counts.Contradicted++
		// Only a Corroborated point clears this streak. Unavailable, unmatched
		// and non-matched points in between leave it standing, so a feed that
		// goes quiet mid-disagreement does not absolve the rider.
		s.contradictions++
	}

	var tr Transition
	if s.state == Pending && s.matchStreak >= verifyStreak {
		s.state = Verified
		tr.StateChanged = true
	}
	if s.contradictions >= contradictStreak {
		s.reject(EndContradicted, at)
		tr.StateChanged = true
		tr.Ended, tr.EndReason = true, s.endReason
	}
	return tr
}

// applyNonMatched folds in a point that failed verification. A run of them
// rejects the ride, blamed on whichever failure dominated the run.
func (s *Session) applyNonMatched(o Outcome, at time.Time) Transition {
	s.matchStreak = 0
	s.nonMatchedStreak = append(s.nonMatchedStreak, o)
	if len(s.nonMatchedStreak) < rejectStreak {
		return Transition{}
	}
	s.reject(endReasonFor(dominantOutcome(s.nonMatchedStreak)), at)
	return Transition{StateChanged: true, Ended: true, EndReason: s.endReason}
}

// reject abandons the ride. The state is Rejected rather than merely ended,
// which is what costs the rider reputation.
func (s *Session) reject(reason EndReason, at time.Time) {
	s.state = Rejected
	s.End(reason, at)
}

// End stops the ride, preserving its state for the summary. The first end wins:
// a client's late "arrived" cannot overwrite a rejection.
func (s *Session) End(reason EndReason, at time.Time) {
	if s.ended {
		return
	}
	s.ended, s.endReason, s.endedAt = true, reason, at
}

// Fresh reports whether the latest accepted point is recent enough to publish.
func (s *Session) Fresh(now time.Time, maxAge time.Duration) bool {
	return s.latest != nil && now.Sub(s.latest.Timestamp) <= maxAge
}

// Publishable reports whether this ride alone may be published as a vehicle
// position. A new rider needs a trusted feed to have corroborated them; a
// trusted rider is taken at their word; a blocked rider is never published.
func (s *Session) Publishable(now time.Time, maxAge time.Duration) bool {
	if s.state != Verified || s.ended || !s.Fresh(now, maxAge) {
		return false
	}
	return s.tier != TierBlocked && (s.tier == TierTrusted || s.corroborated)
}

// RideSummary is what a finished ride amounts to: the input to scoring.
type RideSummary struct {
	State           State
	EndReason       EndReason
	Corroborated    bool
	MatchedDuration time.Duration // last matched timestamp − first matched timestamp
	Counts          Counts
	Duration        time.Duration // EndedAt − StartedAt (0 if not ended)
}

// Summary describes the ride as it stands. It is meaningful before the end, but
// only Duration waits for it.
func (s *Session) Summary() RideSummary {
	sum := RideSummary{
		State:        s.state,
		EndReason:    s.endReason,
		Corroborated: s.corroborated,
		Counts:       s.counts,
	}
	if s.counts.Matched >= 2 {
		sum.MatchedDuration = s.lastMatchedAt.Sub(s.firstMatchedAt)
	}
	if s.ended {
		sum.Duration = s.endedAt.Sub(s.startedAt)
	}
	return sum
}

// dominantOutcome returns the most frequent outcome in a streak, breaking ties
// in favour of the most recent one.
func dominantOutcome(streak []Outcome) Outcome {
	counts := make(map[Outcome]int, len(streak))
	for _, o := range streak {
		counts[o]++
	}
	best := streak[len(streak)-1]
	for i := len(streak) - 1; i >= 0; i-- {
		if counts[streak[i]] > counts[best] {
			best = streak[i]
		}
	}
	return best
}

// endReasonFor names the end a failing outcome causes.
func endReasonFor(o Outcome) EndReason {
	switch o {
	case OffRoute:
		return EndOffRoute
	case Implausible:
		return EndImplausible
	case OffSchedule:
		return EndOffSchedule
	}
	return EndOffRoute
}
