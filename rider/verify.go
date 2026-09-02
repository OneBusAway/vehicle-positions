package rider

import (
	"fmt"
	"math"
	"time"
)

// Thresholds are the tuning knobs of the verification engine. The engine never
// reads them from the environment; callers construct them explicitly, usually
// by adjusting DefaultThresholds.
type Thresholds struct {
	MaxShapeDistance   float64       // how far off the shape a point may be, before its accuracy is added
	MaxSpeed           float64       // metres per second a vehicle may plausibly travel along the shape
	MaxAccuracy        float64       // coarsest GPS accuracy, in metres, still worth verifying
	PastWindow         time.Duration // how stale a reported timestamp may be
	FutureWindow       time.Duration // how far ahead of the clock a reported timestamp may be
	ScheduleEarly      time.Duration // how far ahead of schedule a trip may run
	ScheduleLate       time.Duration // how far behind schedule a trip may run
	PointMaxAge        time.Duration // how long an accepted point stays usable (Session.Fresh / Aggregator)
	MaxRegression      float64       // metres a vehicle may slip backwards along the shape before it is implausible
	CorroborationBase  float64       // along-shape gap to a trusted vehicle that always counts as agreement
	ContradictionExtra float64       // gap beyond the allowance that turns disagreement into contradiction
}

// DefaultThresholds returns the tuning used in production.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MaxShapeDistance:   60,
		MaxSpeed:           35,
		MaxAccuracy:        100,
		PastWindow:         10 * time.Minute,
		FutureWindow:       30 * time.Second,
		ScheduleEarly:      15 * time.Minute,
		ScheduleLate:       90 * time.Minute,
		PointMaxAge:        90 * time.Second,
		MaxRegression:      50,
		CorroborationBase:  150,
		ContradictionExtra: 500,
	}
}

// Point is one location report from a rider. Accuracy, Speed and Bearing are
// -1 when the device did not supply them.
type Point struct {
	Pos                      LatLon
	Accuracy, Speed, Bearing float64
	Timestamp                time.Time
}

// AcceptedPoint is a point that already matched a trip, with the position along
// the shape the match settled on.
type AcceptedPoint struct {
	Point
	AlongShape float64
}

// TrustedVehicle is a position for the same trip from an authoritative feed.
type TrustedVehicle struct {
	VehicleID string
	Pos       LatLon
	Timestamp time.Time
}

// Outcome is the verdict on a single point.
type Outcome int

const (
	// Ignored means the point was unusable and says nothing either way.
	Ignored Outcome = iota
	// OffRoute means the point is too far from the trip's shape.
	OffRoute
	// Implausible means the movement since the previous point cannot happen.
	Implausible
	// OffSchedule means the point is too far from where the trip should be now.
	OffSchedule
	// Matched means the point is consistent with the trip.
	Matched
)

func (o Outcome) String() string {
	switch o {
	case Ignored:
		return "ignored"
	case OffRoute:
		return "off_route"
	case Implausible:
		return "implausible"
	case OffSchedule:
		return "off_schedule"
	case Matched:
		return "matched"
	}
	return "unknown"
}

// Corroboration is how a matched point compares with a trusted feed.
type Corroboration int

const (
	// Unavailable means no trusted position was on hand to compare with.
	Unavailable Corroboration = iota
	// NoCorroboration means the trusted position neither agrees nor conflicts.
	NoCorroboration
	// Corroborated means the trusted position agrees with the point.
	Corroborated
	// Contradicted means the trusted position puts the trip somewhere else.
	Contradicted
)

func (c Corroboration) String() string {
	switch c {
	case Unavailable:
		return "unavailable"
	case NoCorroboration:
		return "none"
	case Corroborated:
		return "corroborated"
	case Contradicted:
		return "contradicted"
	}
	return "unknown"
}

// VerifyInput is everything Verify needs to judge one point.
type VerifyInput struct {
	Trip      *TripInfo
	Timezone  *time.Location
	StartDate string // YYYYMMDD service date the trip started on
	Prev      *AcceptedPoint
	Point     Point
	// Trusted is the agency's own position for the trip, and TrustedAlong is
	// that position projected onto the trip's shape. They travel together:
	// the projection is per trip, not per point, so the caller computes it
	// once for a batch rather than once for every point in it. Both are nil
	// when no trusted vehicle is on hand.
	Trusted      *TrustedVehicle
	TrustedAlong *float64
	Thresholds   Thresholds
	Now          time.Time
}

// Verdict is the result of verifying one point.
type Verdict struct {
	Outcome           Outcome
	Corroboration     Corroboration
	AlongShape        float64
	DistanceToShape   float64
	ScheduleDeviation time.Duration
	Reason            string
}

// Verify judges a single rider point against the trip's shape, its schedule and
// any trusted position for the same trip. Unusable points are Ignored, which is
// neither evidence for nor against the rider being on the trip. AlongShape,
// DistanceToShape and ScheduleDeviation are filled in as far as the point got.
func Verify(in VerifyInput) Verdict {
	if reason, ignored := ignoreReason(in); ignored {
		return Verdict{Outcome: Ignored, Reason: reason}
	}

	// The previous match keeps loops and out-and-backs from snapping to the
	// wrong pass over a point the shape visits twice.
	var hint *float64
	if in.Prev != nil {
		previous := in.Prev.AlongShape
		hint = &previous
	}
	proj := in.Trip.Shape.Project(in.Point.Pos, hint)
	v := Verdict{AlongShape: proj.AlongShape, DistanceToShape: proj.DistanceToShape}

	// A coarse fix is allowed to sit correspondingly further from the shape.
	tolerance := in.Thresholds.MaxShapeDistance + math.Max(in.Point.Accuracy, 0)
	if proj.DistanceToShape > tolerance {
		v.Outcome = OffRoute
		v.Reason = fmt.Sprintf("%.0f m from the shape, tolerance %.0f m", proj.DistanceToShape, tolerance)
		return v
	}

	if in.Prev != nil {
		travelled := proj.AlongShape - in.Prev.AlongShape
		elapsed := in.Point.Timestamp.Sub(in.Prev.Timestamp).Seconds()
		switch {
		case travelled < -in.Thresholds.MaxRegression:
			v.Outcome = Implausible
			v.Reason = fmt.Sprintf("moved %.0f m backwards along the shape", -travelled)
			return v
		case elapsed > 0 && math.Abs(travelled)/elapsed > in.Thresholds.MaxSpeed:
			v.Outcome = Implausible
			v.Reason = fmt.Sprintf("implied speed %.0f m/s over %.0f s", math.Abs(travelled)/elapsed, elapsed)
			return v
		}
	}

	dayStart, err := ServiceDayStart(in.StartDate, in.Timezone)
	if err != nil {
		v.Outcome = OffSchedule
		v.Reason = "bad start_date"
		return v
	}
	scheduled := dayStart.Add(ScheduledOffsetAt(in.Trip, proj.AlongShape))
	v.ScheduleDeviation = in.Point.Timestamp.Sub(scheduled)
	if v.ScheduleDeviation < -in.Thresholds.ScheduleEarly || v.ScheduleDeviation > in.Thresholds.ScheduleLate {
		v.Outcome = OffSchedule
		v.Reason = fmt.Sprintf("%s from the scheduled position", v.ScheduleDeviation.Round(time.Second))
		return v
	}

	v.Outcome = Matched
	v.Corroboration = corroborate(in, proj.AlongShape)
	return v
}

// ignoreReason reports whether a point is unusable, and why.
func ignoreReason(in VerifyInput) (string, bool) {
	if in.Trip == nil || in.Trip.Shape == nil {
		return "trip has no shape", true
	}
	if !plausiblePos(in.Point.Pos) {
		return "invalid coordinates", true
	}
	if in.Point.Accuracy > in.Thresholds.MaxAccuracy {
		return fmt.Sprintf("accuracy > %.0f m", in.Thresholds.MaxAccuracy), true
	}
	if age := in.Now.Sub(in.Point.Timestamp); age > in.Thresholds.PastWindow || age < -in.Thresholds.FutureWindow {
		return "timestamp outside window", true
	}
	if in.Prev != nil && !in.Point.Timestamp.After(in.Prev.Timestamp) {
		return "not newer than previous point", true
	}
	return "", false
}

// corroborate compares a matched position with the trusted feed. The allowance
// grows with the age difference between the two fixes, because a trip keeps
// moving between them.
func corroborate(in VerifyInput, along float64) Corroboration {
	if in.Trusted == nil || in.TrustedAlong == nil {
		return Unavailable
	}
	gap := math.Abs(*in.TrustedAlong - along)
	skew := in.Trusted.Timestamp.Sub(in.Point.Timestamp).Abs()
	allowance := in.Thresholds.CorroborationBase + in.Thresholds.MaxSpeed*skew.Seconds()

	switch {
	case gap <= allowance:
		return Corroborated
	case gap > allowance+in.Thresholds.ContradictionExtra:
		return Contradicted
	default:
		return NoCorroboration
	}
}

// plausiblePos reports whether a coordinate could be a real fix. Null island is
// what a broken or zero-valued fix reports, never a bus.
func plausiblePos(p LatLon) bool {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) {
		return false
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return false
	}
	return p.Lat != 0 || p.Lon != 0
}
