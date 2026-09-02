package rider

import (
	"cmp"
	"errors"
	"math"
	"slices"
	"sync"
	"time"
)

// ErrUnknownRide is returned for a ride id that is not registered, or whose
// ride has already ended. Either way the client must start a new ride.
var ErrUnknownRide = errors.New("unknown or ended ride")

const (
	// idleTimeout is how long a ride may go without an accepted point before
	// the aggregator gives up on it.
	idleTimeout = 15 * time.Minute
	// maxRideDuration caps a ride: no scheduled trip a rider can join runs
	// this long, so a session older than this is a client that never stopped.
	maxRideDuration = 3 * time.Hour
	// consensusDistance is how close two riders' positions must be for them to
	// vouch for each other in the absence of a trusted feed.
	consensusDistance = 100.0
	// outlierDistance is how far a rider may sit from the group's median
	// before that rider is dropped from the estimate.
	outlierDistance = 100.0
)

// AppliedPoint is one point of a batch together with the verdict it earned. The
// caller persists these; the engine keeps only what the session folded in.
type AppliedPoint struct {
	Point
	Verdict Verdict
}

// BatchResult is what applying a batch of points did to a ride.
type BatchResult struct {
	State          State
	Published      bool          // Session.Publishable at the end of the batch
	Corroboration  Corroboration // Session.LatestCorroboration
	Accepted       int           // non-ignored points applied
	Ignored        int
	OffRouteStreak int
	Ended          bool
	EndReason      EndReason
	Points         []AppliedPoint // non-ignored points, in application order (for persistence)
	Counts         Counts
	Corroborated   bool
}

// TripEstimate is the vehicle position the riders of one trip add up to.
type TripEstimate struct {
	Key          TripKey
	RouteID      string
	Pos          LatLon
	Bearing      float64
	Speed        *float64
	Timestamp    time.Time
	StopID       string // "" past the last stop
	StopSequence int    // 0 when StopID == ""
	Riders       int
}

// Aggregator is the registry of live rides. It owns every Session it holds and
// serialises access to them, so a session's own methods need no locking.
type Aggregator struct {
	th Thresholds
	tz *time.Location

	mu       sync.Mutex
	sessions map[string]*Session // by ride id
	byRider  map[string]string   // rider id → the ride id of that rider's newest session
}

// NewAggregator returns an empty registry judging points with th, in tz.
func NewAggregator(th Thresholds, tz *time.Location) *Aggregator {
	return &Aggregator{
		th:       th,
		tz:       tz,
		sessions: make(map[string]*Session),
		byRider:  make(map[string]string),
	}
}

// Add registers a session, replacing any session with the same ride id and
// making it the rider's active ride. An earlier ride of the same rider stays
// registered until it is removed, so its points can still be reaped and
// persisted; it is simply no longer the rider's active one.
func (a *Aggregator) Add(s *Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[s.ID()] = s
	a.byRider[s.RiderID()] = s.ID()
}

// Owner returns the rider a live ride belongs to. An ended ride has no owner:
// nobody may post to it any more.
func (a *Aggregator) Owner(rideID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[rideID]
	if !ok || s.Ended() {
		return "", false
	}
	return s.RiderID(), true
}

// Session returns the registered session of a ride, ended or not. Owner
// answers who may post to a ride; this is for the caller that has to persist
// what a ride amounted to, which an ended-but-not-yet-removed ride still needs.
func (a *Aggregator) Session(rideID string) (*Session, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[rideID]
	return s, ok
}

// ActiveRideForRider returns the rider's newest live ride, if they have one.
func (a *Aggregator) ActiveRideForRider(riderID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.activeForRiderLocked(riderID)
	if s == nil {
		return "", false
	}
	return s.ID(), true
}

// SetTier updates the tier of the rider's active ride, so a score change takes
// effect on the ride in flight rather than only on the next one.
func (a *Aggregator) SetTier(riderID string, tier Tier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.activeForRiderLocked(riderID); s != nil {
		s.SetTier(tier)
	}
}

// Remove unregisters a ride and returns its session, or nil if there was none.
func (a *Aggregator) Remove(rideID string) *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.removeLocked(rideID)
}

// ActiveCount is the number of registered rides that have not ended.
func (a *Aggregator) ActiveCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, s := range a.sessions {
		if !s.Ended() {
			n++
		}
	}
	return n
}

// ApplyBatch verifies a batch of points against the ride's trip and folds them
// into its session, oldest first. Points that arrive after the ride ends
// mid-batch are ignored rather than applied, because the ride is over. lookup
// supplies the trusted position for the trip and may be nil.
func (a *Aggregator) ApplyBatch(rideID string, points []Point, lookup func(TripKey) (TrustedVehicle, bool), now time.Time) (BatchResult, error) {
	key, err := a.liveKey(rideID)
	if err != nil {
		return BatchResult{}, err
	}

	// lookup belongs to the caller and may take locks of its own, so it is
	// called before the registry lock is taken rather than under it. The
	// trusted position is per trip, not per point, so once is enough.
	var trusted *TrustedVehicle
	if lookup != nil {
		if v, ok := lookup(key); ok {
			trusted = &v
		}
	}

	sorted := slices.Clone(points)
	slices.SortStableFunc(sorted, func(x, y Point) int { return x.Timestamp.Compare(y.Timestamp) })

	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[rideID]
	// The ride may have ended, been removed, or been replaced by a ride on a
	// different trip while the lock was released for lookup; a trusted position
	// fetched for the old trip says nothing about the new one.
	if !ok || s.Ended() || s.Key() != key {
		return BatchResult{}, ErrUnknownRide
	}

	res := BatchResult{}
	for i, p := range sorted {
		if s.Ended() {
			res.Ignored += len(sorted) - i
			break
		}
		v := Verify(VerifyInput{
			Trip:      s.Trip(),
			Timezone:  a.tz,
			StartDate: s.Key().StartDate,
			// The baseline is the latest point whose geometry matched, not the
			// latest accepted point: an off-route or implausible point is
			// exactly the position the next point must not be judged against.
			Prev:       s.LatestMatched(),
			Point:      p,
			Trusted:    trusted,
			Thresholds: a.th,
			Now:        now,
		})
		if v.Outcome == Ignored {
			res.Ignored++
			continue
		}
		s.Apply(v, p)
		res.Accepted++
		res.Points = append(res.Points, AppliedPoint{Point: p, Verdict: v})
	}

	res.State = s.State()
	res.Published = s.Publishable(now, a.th.PointMaxAge)
	res.Corroboration = s.LatestCorroboration()
	res.OffRouteStreak = s.OffRouteStreak()
	res.Ended = s.Ended()
	res.EndReason = s.EndReason()
	res.Counts = s.Counts()
	res.Corroborated = s.Corroborated()
	return res, nil
}

// Reap ends and unregisters the rides that have run out of time: one that has
// gone quiet for idleTimeout, and one that has been going for longer than any
// trip could last. The returned sessions are the caller's to persist.
func (a *Aggregator) Reap(now time.Time) []*Session {
	a.mu.Lock()
	defer a.mu.Unlock()

	var reaped []*Session
	for id, s := range a.sessions {
		reason, expired := expiry(s, now)
		if !expired {
			continue
		}
		s.End(reason, now)
		a.removeLocked(id)
		reaped = append(reaped, s)
	}
	slices.SortFunc(reaped, func(x, y *Session) int { return cmp.Compare(x.ID(), y.ID()) })
	return reaped
}

// Estimates returns one vehicle position per trip the riders can vouch for,
// sorted by trip id then start date. Trips the trusted feed already covers are
// left out: the agency's own position wins.
func (a *Aggregator) Estimates(now time.Time, covered func(TripKey) bool) []TripEstimate {
	groups := a.groups(now)
	out := make([]TripEstimate, 0, len(groups))
	for key, g := range groups {
		if covered != nil && covered(key) {
			continue
		}
		if est, ok := g.estimate(key, now); ok {
			out = append(out, est)
		}
	}
	slices.SortFunc(out, func(x, y TripEstimate) int {
		return cmp.Or(
			cmp.Compare(x.Key.TripID, y.Key.TripID),
			cmp.Compare(x.Key.StartDate, y.Key.StartDate),
		)
	})
	return out
}

// PublishableCount is how many trips currently have a rider-derived position.
func (a *Aggregator) PublishableCount(now time.Time, covered func(TripKey) bool) int {
	return len(a.Estimates(now, covered))
}

// TripStatus answers what the riders are saying about one trip: whether their
// position is being published for it, and how many riders are contributing to
// it. riders counts the contributing sessions — live, verified, not blocked and
// fresh — as they stand before the outlier trim, so it can exceed the Riders of
// the TripEstimate for the same trip, which counts only the riders left after
// the trim. A rider whose points have gone stale counts in neither.
func (a *Aggregator) TripStatus(key TripKey, now time.Time, covered func(TripKey) bool) (riderReported bool, riders int) {
	g, ok := a.groups(now)[key]
	if !ok {
		return false, 0
	}
	riders = distinctRiders(g.members)
	if covered != nil && covered(key) {
		return false, riders
	}
	_, reported := g.estimate(key, now)
	return reported, riders
}

// activeForRiderLocked returns the rider's newest live session, or nil.
func (a *Aggregator) activeForRiderLocked(riderID string) *Session {
	rideID, ok := a.byRider[riderID]
	if !ok {
		return nil
	}
	s, ok := a.sessions[rideID]
	if !ok || s.Ended() {
		return nil
	}
	return s
}

// removeLocked unregisters a ride, clearing the rider's active ride only if it
// still points at this one.
func (a *Aggregator) removeLocked(rideID string) *Session {
	s, ok := a.sessions[rideID]
	if !ok {
		return nil
	}
	delete(a.sessions, rideID)
	if a.byRider[s.RiderID()] == rideID {
		delete(a.byRider, s.RiderID())
	}
	return s
}

// liveKey returns the trip a live ride is on, so the trusted feed can be
// consulted before the registry lock is taken.
func (a *Aggregator) liveKey(rideID string) (TripKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[rideID]
	if !ok || s.Ended() {
		return TripKey{}, ErrUnknownRide
	}
	return s.Key(), nil
}

// expiry reports whether a ride has run out of time, and why. The max-duration
// rule is checked first: a ride that has been going for hours is over on those
// grounds however recently it reported.
func expiry(s *Session, now time.Time) (EndReason, bool) {
	if now.Sub(s.StartedAt()) > maxRideDuration {
		return EndMaxDuration, true
	}
	last := s.LastAcceptedAt()
	if last.IsZero() {
		last = s.StartedAt()
	}
	if now.Sub(last) > idleTimeout {
		return EndIdle, true
	}
	return "", false
}

// estimateMember is one rider's contribution to a trip estimate, copied out of
// the session so the estimate can be computed without holding the lock. It is
// the rider's latest *matched* point: an off-route or implausible point is not
// a position anyone should be told the vehicle is at.
type estimateMember struct {
	riderID     string
	along       float64
	speed       float64
	timestamp   time.Time
	publishable bool
}

// tripGroup is the riders contributing to one trip: those whose ride is live,
// verified, not blocked, and whose latest point is fresh enough to position the
// vehicle with. Every consumer of a group — the estimate and the trip status —
// works from this one selection, so the two cannot disagree about who counts.
// Freshness is judged on the latest point of any kind, because a rider still
// reporting is still there; the position itself comes from the latest matched
// one.
type tripGroup struct {
	trip    *TripInfo
	members []estimateMember
}

// groups snapshots the contributing rides by trip.
func (a *Aggregator) groups(now time.Time) map[TripKey]*tripGroup {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make(map[TripKey]*tripGroup)
	for _, s := range a.sessions {
		if s.Ended() || s.Tier() == TierBlocked || s.State() != Verified || !s.Fresh(now, a.th.PointMaxAge) {
			continue
		}
		// A verified session always has a matched point; the check is what makes
		// that an invariant of the group rather than an assumption.
		matched := s.LatestMatched()
		if matched == nil {
			continue
		}
		g, ok := out[s.Key()]
		if !ok {
			g = &tripGroup{trip: s.Trip()}
			out[s.Key()] = g
		}
		g.members = append(g.members, estimateMember{
			riderID:     s.RiderID(),
			along:       matched.AlongShape,
			speed:       matched.Speed,
			timestamp:   matched.Timestamp,
			publishable: s.Publishable(now, a.th.PointMaxAge),
		})
	}
	return out
}

// estimate combines the group's members into one vehicle position, reporting
// false when the riders are not credible enough to publish.
func (g *tripGroup) estimate(key TripKey, now time.Time) (TripEstimate, bool) {
	if g.trip == nil || g.trip.Shape == nil || !g.publishable() {
		return TripEstimate{}, false
	}

	members := g.members
	median := medianAlong(members)
	// A rider far from the median is on a different vehicle, or lost; drop them
	// and re-centre on what is left. Once only: the survivors are the estimate.
	if kept := within(members, median, outlierDistance); len(kept) > 0 && len(kept) < len(members) {
		members = kept
		median = medianAlong(members)
	}

	est := TripEstimate{
		Key:       key,
		RouteID:   g.trip.RouteID,
		Pos:       g.trip.Shape.PointAt(median),
		Bearing:   g.trip.Shape.BearingAt(median),
		Speed:     medianSpeed(members),
		Timestamp: newestTimestamp(members, now),
		Riders:    distinctRiders(members),
	}
	if stop, ok := nextStop(g.trip, median); ok {
		est.StopID, est.StopSequence = stop.StopID, stop.Sequence
	}
	return est, true
}

// publishable reports whether the group may be published: either one rider is
// trusted enough on their own, or two different riders put the vehicle in the
// same place, which is the crowd standing in for a trusted feed.
func (g *tripGroup) publishable() bool {
	for _, m := range g.members {
		if m.publishable {
			return true
		}
	}
	for i, a := range g.members {
		for _, b := range g.members[i+1:] {
			if a.riderID != b.riderID && math.Abs(a.along-b.along) <= consensusDistance {
				return true
			}
		}
	}
	return false
}

// within returns the members no further than limit from along.
func within(members []estimateMember, along, limit float64) []estimateMember {
	kept := make([]estimateMember, 0, len(members))
	for _, m := range members {
		if math.Abs(m.along-along) <= limit {
			kept = append(kept, m)
		}
	}
	return kept
}

// medianAlong is the median position along the shape of the members.
func medianAlong(members []estimateMember) float64 {
	alongs := make([]float64, len(members))
	for i, m := range members {
		alongs[i] = m.along
	}
	return median(alongs)
}

// medianSpeed is the median of the members that reported a speed, or nil when
// none did.
func medianSpeed(members []estimateMember) *float64 {
	var speeds []float64
	for _, m := range members {
		if m.speed >= 0 {
			speeds = append(speeds, m.speed)
		}
	}
	if len(speeds) == 0 {
		return nil
	}
	v := median(speeds)
	return &v
}

// newestTimestamp is the freshest member's timestamp, never in the future: a
// device clock running fast must not date a position ahead of the server.
func newestTimestamp(members []estimateMember, now time.Time) time.Time {
	newest := members[0].timestamp
	for _, m := range members[1:] {
		if m.timestamp.After(newest) {
			newest = m.timestamp
		}
	}
	if newest.After(now) {
		return now
	}
	return newest
}

// distinctRiders counts how many different people the members are.
func distinctRiders(members []estimateMember) int {
	seen := make(map[string]bool, len(members))
	for _, m := range members {
		seen[m.riderID] = true
	}
	return len(seen)
}

// nextStop is the first stop of the trip at or ahead of `along`; there is none
// once the vehicle is past the last stop.
func nextStop(trip *TripInfo, along float64) (StopTimeInfo, bool) {
	for _, st := range trip.StopTimes {
		if st.AlongShape >= along {
			return st, true
		}
	}
	return StopTimeInfo{}, false
}

// median returns the middle value, averaging the middle two when there is an
// even number of them. It does not disturb the caller's slice.
func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
