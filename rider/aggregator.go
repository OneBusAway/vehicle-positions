package rider

import (
	"cmp"
	"errors"
	"log/slog"
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

// BatchResult is what applying a batch of points did to a ride. Everything the
// ride has amounted to so far — its state, whether and why it ended, its counts
// and corroboration — is in Summary, and only there, so a caller cannot read
// the state from one place and the end reason from another; the rest describes
// this batch and the session's standing at the end of it.
type BatchResult struct {
	Published      bool          // Session.Publishable at the end of the batch
	Corroboration  Corroboration // Session.LatestCorroboration
	Accepted       int           // non-ignored points applied
	Ignored        int
	OffRouteStreak int
	Points         []AppliedPoint // non-ignored points, in application order (for persistence)
	Summary        RideSummary
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

// RideSnapshot is everything a caller outside this package needs to know about
// a live ride, copied out by value. It exists so that no registered session's
// pointer ever escapes the aggregator: a registered session is read and written
// only under a.mu, and a snapshot is the way to read one.
type RideSnapshot struct {
	ID        string
	RiderID   string
	Key       TripKey
	Tier      Tier
	StartedAt time.Time
	Ended     bool
	// Summary is everything the ride amounts to — its state, why it ended, its
	// counts. It is the single carrier of those facts, so a caller cannot read
	// a state from one field and counts from another that disagree.
	Summary RideSummary
}

// Snapshot copies out the state of a registered ride, whether or not it has
// ended: an ended ride is still registered until its outcome is persisted, and
// that is exactly the caller that needs this. Unknown ride ids report false.
// Taking the snapshot under the lock is what lets a caller decide what a ride
// amounted to while batches are still being applied to other rides.
func (a *Aggregator) Snapshot(rideID string) (RideSnapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[rideID]
	if !ok {
		return RideSnapshot{}, false
	}
	return RideSnapshot{
		ID:        s.ID(),
		RiderID:   s.RiderID(),
		Key:       s.Key(),
		Tier:      s.Tier(),
		StartedAt: s.StartedAt(),
		Ended:     s.Ended(),
		Summary:   s.Summary(),
	}, true
}

// End ends a registered ride in place under the lock and returns what it
// amounted to. The first end wins: a ride that ended itself — rejected, or
// reaped — keeps its own reason, and the snapshot reports it. The session
// stays registered, exactly as after Reap, until the caller has persisted the
// outcome and Removes it; meanwhile it no longer counts as active, publishes
// nothing and answers ErrUnknownRide to a batch, so nothing can be folded into
// a ride between its outcome being read and its being filed. Unknown ride ids
// report false.
func (a *Aggregator) End(rideID string, reason EndReason, now time.Time) (RideSnapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[rideID]
	if !ok {
		return RideSnapshot{}, false
	}
	s.End(reason, now)
	return RideSnapshot{
		ID:        s.ID(),
		RiderID:   s.RiderID(),
		Key:       s.Key(),
		Tier:      s.Tier(),
		StartedAt: s.StartedAt(),
		Ended:     true,
		Summary:   s.Summary(),
	}, true
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

	// The trusted position is per trip, not per point: projecting it onto the
	// shape once here spares the projection on every point of the batch. The
	// rider's last match is the hint, for the same reason it is the hint for
	// their own points: on a loop or an out-and-back the globally closest
	// segment may be the other pass, and a rider actually aboard would then
	// be contradicted point after point.
	var trustedAlong *float64
	if trusted != nil && s.Trip() != nil && s.Trip().Shape != nil {
		var hint *float64
		if prev := s.LatestMatched(); prev != nil {
			prevAlong := prev.AlongShape
			hint = &prevAlong
		}
		along := s.Trip().Shape.Project(trusted.Pos, hint).AlongShape
		trustedAlong = &along
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
			Prev:         s.LatestMatched(),
			LastAccepted: s.LastAcceptedAt(),
			Point:        p,
			Trusted:      trusted,
			TrustedAlong: trustedAlong,
			Thresholds:   a.th,
			Now:          now,
		})
		if v.Outcome == Ignored {
			res.Ignored++
			slog.Debug("rider: point ignored", "ride_id", rideID, "reason", v.Reason)
			continue
		}
		if v.Outcome != Matched {
			// The verdict's reason is the only account of *why* a point failed;
			// nothing else records it, so it is logged here rather than lost.
			slog.Debug("rider: point did not match the trip", "ride_id", rideID,
				"outcome", v.Outcome.String(), "reason", v.Reason)
		}
		s.Apply(v, p)
		res.Accepted++
		res.Points = append(res.Points, AppliedPoint{Point: p, Verdict: v})
	}

	res.Published = s.Publishable(now, a.th.PointMaxAge)
	res.Corroboration = s.LatestCorroboration()
	res.OffRouteStreak = s.OffRouteStreak()
	res.Summary = s.Summary()
	return res, nil
}

// Reap ends, in place, the rides that have run out of time: one that has gone
// quiet for idleTimeout, and one that has been going for longer than any trip
// could last. It returns the ride ids of every ended-but-still-registered
// session — the ones it just ended, and any ended earlier whose outcome has
// not been persisted yet — so the caller can file each one through the single
// ride-ending path and retry the ones that failed. Nothing is unregistered
// here: the session goes only once the store has accepted its outcome. An
// ended session no longer counts as active, publishes nothing and cannot be
// posted to, so leaving it registered changes nothing but where it is filed
// from.
func (a *Aggregator) Reap(now time.Time) []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	var reaped []string
	for id, s := range a.sessions {
		if !s.Ended() {
			reason, expired := expiry(s, now)
			if !expired {
				continue
			}
			s.End(reason, now)
		}
		reaped = append(reaped, id)
	}
	slices.Sort(reaped)
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

// PublishableCount is how many trips currently have a rider-derived position:
// the trips Estimates would publish, counted rather than re-derived, so the
// count can never disagree with the feed.
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
		m, ok := a.memberOfLocked(s, now)
		if !ok {
			continue
		}
		g := out[s.Key()]
		if g == nil {
			g = &tripGroup{trip: s.Trip()}
			out[s.Key()] = g
		}
		g.members = append(g.members, m)
	}
	return out
}

// memberOfLocked is one session's contribution to its trip's group, and
// whether it contributes at all. It is the single definition of "counts
// towards this trip".
func (a *Aggregator) memberOfLocked(s *Session, now time.Time) (estimateMember, bool) {
	if s.Ended() || s.Tier() == TierBlocked || s.State() != Verified || !s.Fresh(now, a.th.PointMaxAge) {
		return estimateMember{}, false
	}
	// A verified session always has a matched point; the check is what makes
	// that an invariant of the group rather than an assumption.
	matched := s.LatestMatched()
	if matched == nil {
		return estimateMember{}, false
	}
	return estimateMember{
		riderID:     s.RiderID(),
		along:       matched.AlongShape,
		speed:       matched.Speed,
		timestamp:   matched.Timestamp,
		publishable: s.Publishable(now, a.th.PointMaxAge),
	}, true
}

// estimate combines the group's members into one vehicle position, reporting
// false when the riders are not credible enough to publish.
func (g *tripGroup) estimate(key TripKey, now time.Time) (TripEstimate, bool) {
	if !g.canEstimate() {
		return TripEstimate{}, false
	}

	members := g.members
	median := medianAlong(members)
	// A rider far from the median is on a different vehicle, or lost; drop them
	// and re-centre on what is left. Once only: the survivors are the estimate.
	if kept := within(members, median, outlierDistance); len(kept) > 0 && len(kept) < len(members) {
		// The trim may have dropped the very rider the group was publishable
		// on — a trusted rider the crowd outvoted — and the survivors must
		// then be credible on their own, or nothing is.
		if !(&tripGroup{trip: g.trip, members: kept}).publishable() {
			return TripEstimate{}, false
		}
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

// canEstimate reports whether the group can and may be turned into a position:
// it has the geometry to place one on, and its riders are credible enough to
// publish. It is the whole of estimate's precondition, so counting the groups
// that satisfy it counts exactly the estimates Estimates would produce.
func (g *tripGroup) canEstimate() bool {
	return g.trip != nil && g.trip.Shape != nil && g.publishable()
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
