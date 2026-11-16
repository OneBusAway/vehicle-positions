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
	// StartTime and FrequencyBased describe the run the riders are on
	// (TripInfo.Run): its start_time, "" for a trip run by timetable, and
	// whether it is run by headway.
	StartTime      string
	FrequencyBased bool
}

// Aggregator is the registry of live rides. It owns every Session it holds and
// serialises access to them, so a session's own methods need no locking.
type Aggregator struct {
	th Thresholds
	tz *time.Location

	mu       sync.Mutex
	sessions map[string]*Session // by ride id
	byRider  map[string]string   // rider id → the ride id of that rider's newest session
	// runs is the runs of each trip its riders are on, as runStarts keeps
	// them. A trip's entry goes when the last ride on it is removed.
	runs map[TripKey][]riderRun
}

// riderRun is when a run of a trip its riders are on left its first stop,
// and where along the shape and when its latest estimate put it.
type riderRun struct {
	start time.Time
	along float64
	at    time.Time
}

// NewAggregator returns an empty registry judging points with th, in tz.
func NewAggregator(th Thresholds, tz *time.Location) *Aggregator {
	return &Aggregator{
		th:       th,
		tz:       tz,
		sessions: make(map[string]*Session),
		byRider:  make(map[string]string),
		runs:     make(map[TripKey][]riderRun),
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

// Estimates returns one vehicle position per run of a trip the riders can
// vouch for, sorted by trip id, start date and start time. Trips the trusted
// feed already covers are left out: the agency's own position wins.
func (a *Aggregator) Estimates(now time.Time, covered func(TripKey) bool) []TripEstimate {
	groups := a.groups(now)
	out := make([]TripEstimate, 0, len(groups))
	for key, g := range groups {
		if covered != nil && covered(key) {
			continue
		}
		out = append(out, a.tripEstimates(key, g, now)...)
	}
	slices.SortFunc(out, func(x, y TripEstimate) int {
		return cmp.Or(
			cmp.Compare(x.Key.TripID, y.Key.TripID),
			cmp.Compare(x.Key.StartDate, y.Key.StartDate),
			cmp.Compare(x.StartTime, y.StartTime),
		)
	})
	return out
}

// tripEstimates is an estimate for each run of key's trip that g's riders
// can vouch for. Two runs that come out at the same start_time are one trip
// instance to GTFS-RT, which a feed may name once, so only the one more
// riders are on is kept. A trip run by timetable has one run, so its estimate
// is never one of two.
func (a *Aggregator) tripEstimates(key TripKey, g *tripGroup, now time.Time) []TripEstimate {
	var ests []TripEstimate
	var places []runPlace
	for _, run := range g.runs() {
		est, along, ok := run.estimate(key, now)
		if !ok {
			continue
		}
		ests = append(ests, est)
		places = append(places, runPlace{group: run, along: along, at: est.Timestamp})
	}
	out := make([]TripEstimate, 0, len(ests))
	for i, start := range a.runStarts(key, g.trip, places, now) {
		est := ests[i]
		est.StartTime, est.FrequencyBased = g.trip.Run(key.StartDate, a.tz, start)
		j := slices.IndexFunc(out, func(o TripEstimate) bool { return o.StartTime == est.StartTime })
		switch {
		case j < 0:
			out = append(out, est)
		case est.Riders > out[j].Riders:
			out[j] = est
		}
	}
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
	for _, run := range g.runs() {
		if _, _, ok := run.estimate(key, now); ok {
			return true, riders
		}
	}
	return false, riders
}

// runPlace is where an estimate put one run of a trip, and when, with the
// riders who put it there.
type runPlace struct {
	group *tripGroup
	along float64
	at    time.Time
}

// runStarts is when each of places' runs of key's trip left its first stop.
// A run's start is the schedule's time from its first departure to where its
// riders put it, back from when they did. Riders board along the way, so none
// of them saw it start; a vehicle they put at its first stop is taken to be
// leaving it then, however long the stop's dwell.
//
// GTFS-RT fixes a headway run's start_time once it is published, and the
// riders' later positions would each put the start somewhere else, so a run's
// first start is kept and each place is matched to a kept run. A kept run
// can only be where its last estimate's vehicle could be since: no more than
// CorroborationBase back along the shape, the trusted feed's allowance for
// two positions of one vehicle, and no further ahead than that plus MaxSpeed
// for the time between them. An older estimate, which reaches here after a
// newer one when two Estimates overlap, is held to the same reach back in
// time. Of the kept runs a place could be, it takes the one whose start is
// nearest its own, and each kept run goes to one place at most; a place that
// takes none is another run, with its own start. A kept run that no estimate
// has placed for idleTimeout, the time a ride may go without a point, has no
// riders left and is let go, so a later run does not take its start.
//
// groups lets go of a.mu before this takes it, so a place's rides may have
// been removed meanwhile. A new run is kept only if one of them is still
// registered: one kept for rides that have gone, or been replaced, would be
// passed on to riders it does not describe.
func (a *Aggregator) runStarts(key TripKey, trip *TripInfo, places []runPlace, now time.Time) []time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := slices.DeleteFunc(a.runs[key], func(r riderRun) bool { return now.Sub(r.at) > idleTimeout })

	starts := make([]time.Time, len(places))
	for i, p := range places {
		starts[i] = p.at.Add(-max(0, ScheduledOffsetAt(trip, p.along)-trip.StopTimes[0].Departure))
	}
	type match struct {
		place, run int
		gap        time.Duration
	}
	var matches []match
	for i, p := range places {
		for j, r := range kept {
			if a.couldBe(r, p) {
				matches = append(matches, match{i, j, starts[i].Sub(r.start).Abs()})
			}
		}
	}
	slices.SortStableFunc(matches, func(x, y match) int { return cmp.Compare(x.gap, y.gap) })
	placed := make([]bool, len(places))
	taken := make([]bool, len(kept))
	for _, m := range matches {
		if placed[m.place] || taken[m.run] {
			continue
		}
		placed[m.place], taken[m.run] = true, true
		r, p := &kept[m.run], places[m.place]
		starts[m.place] = r.start
		if !p.at.Before(r.at) {
			r.along, r.at = p.along, p.at
		}
	}
	for i, p := range places {
		if !placed[i] && slices.ContainsFunc(p.group.members, func(m estimateMember) bool { return a.sessions[m.rideID] != nil }) {
			kept = append(kept, riderRun{start: starts[i], along: p.along, at: p.at})
		}
	}
	if len(kept) == 0 {
		delete(a.runs, key)
	} else {
		a.runs[key] = kept
	}
	return starts
}

// couldBe reports whether r's vehicle could be where p puts a run: forward in
// time, no more than CorroborationBase back from r's last place and no
// further ahead than that plus MaxSpeed for the time since; back in time, the
// other way round.
func (a *Aggregator) couldBe(r riderRun, p runPlace) bool {
	moved := p.along - r.along
	elapsed := p.at.Sub(r.at).Seconds()
	if elapsed < 0 {
		moved, elapsed = -moved, -elapsed
	}
	return moved >= -a.th.CorroborationBase && moved <= a.th.CorroborationBase+a.th.MaxSpeed*elapsed
}

// rideOnLocked reports whether a ride on key is registered.
func (a *Aggregator) rideOnLocked(key TripKey) bool {
	for _, s := range a.sessions {
		if s.Key() == key {
			return true
		}
	}
	return false
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
	if !a.rideOnLocked(s.Key()) {
		delete(a.runs, s.Key())
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
	rideID      string
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
// Freshness is judged on the latest *matched* point, which is also the point
// the position comes from (Session.Fresh): a run of off-route points too short
// to reject the ride must not keep a minutes-old match looking live.
type tripGroup struct {
	trip    *TripInfo
	members []estimateMember
}

// runs splits the group into the runs of its trip that its riders are on. A
// trip in frequencies.txt runs again and again on a service day, every run
// under the one TripKey, so riders of two runs at once are one group. They
// are told apart along the shape: a run ends wherever the next rider is more
// than outlierDistance on, the distance at which an estimate takes a rider
// for one on another vehicle. A trip run by timetable has one run a day, and
// its riders stay one group, as far apart as they are.
func (g *tripGroup) runs() []*tripGroup {
	if len(g.trip.frequencies) == 0 {
		return []*tripGroup{g}
	}
	members := slices.SortedFunc(slices.Values(g.members), func(x, y estimateMember) int { return cmp.Compare(x.along, y.along) })
	var runs []*tripGroup
	for i, m := range members {
		if i == 0 || m.along-members[i-1].along > outlierDistance {
			runs = append(runs, &tripGroup{trip: g.trip})
		}
		runs[len(runs)-1].members = append(runs[len(runs)-1].members, m)
	}
	return runs
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
	// Add leaves a rider's earlier ride registered so its points can still be
	// reaped, so "registered and live" is not the same as "this rider's ride".
	// One rider must contribute one along value: two would let a single phone
	// weight the median twice and survive outlier trimming as a pair, which
	// distinctRiders hides from the count but not from the estimate.
	if a.byRider[s.RiderID()] != s.ID() {
		return estimateMember{}, false
	}
	// A verified session always has a matched point; the check is what makes
	// that an invariant of the group rather than an assumption.
	matched := s.LatestMatched()
	if matched == nil {
		return estimateMember{}, false
	}
	return estimateMember{
		rideID:      s.ID(),
		riderID:     s.RiderID(),
		along:       matched.AlongShape,
		speed:       matched.Speed,
		timestamp:   matched.Timestamp,
		publishable: s.Publishable(now, a.th.PointMaxAge),
	}, true
}

// estimate combines the group's members into one vehicle position, and how
// far along the trip's shape it is, reporting false when the riders are not
// credible enough to publish.
func (g *tripGroup) estimate(key TripKey, now time.Time) (TripEstimate, float64, bool) {
	if !g.canEstimate() {
		return TripEstimate{}, 0, false
	}

	members := g.members
	median := medianAlong(members)
	// A rider far from the median is on a different vehicle, or lost; drop them
	// and re-centre on what is left. Once only: the survivors are the estimate.
	if kept := within(members, median, outlierDistance); len(kept) < len(members) {
		// An even-sized group split further apart than twice the allowance
		// averages to a median no rider is near, and the trim then keeps
		// nobody. There is no consensus position to publish: the midpoint
		// between two riders on different vehicles is where neither is.
		//
		// The trim may also have dropped the very rider the group was
		// publishable on — a trusted rider the crowd outvoted — and the
		// survivors must then be credible on their own, or nothing is.
		if len(kept) == 0 || !(&tripGroup{trip: g.trip, members: kept}).publishable() {
			return TripEstimate{}, 0, false
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
	if stop, ok := NextStop(g.trip, median); ok {
		est.StopID, est.StopSequence = stop.StopID, stop.Sequence
	}
	return est, median, true
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

// NextStop is the first stop of the trip at or ahead of `along`; there is none
// once the vehicle is past the last stop.
func NextStop(trip *TripInfo, along float64) (StopTimeInfo, bool) {
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
