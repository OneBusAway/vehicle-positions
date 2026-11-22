package main

import (
	"math"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// stoppedAtDistance is how far along the shape, either side of a stop, a
// driver vehicle may be for the feed to say it is STOPPED_AT the stop rather
// than IN_TRANSIT_TO it. None of the rider thresholds measures this: their
// distances say how far a point may be from the route, from its last match or
// from a trusted vehicle, not how near counts as being at a stop. 30 m holds a
// 12 to 18 m bus pulled up anywhere along a stop, plus a phone's GPS error,
// and is still well short of the distance between two stops of one trip;
// where two stops are closer than that, the earlier one wins. It counts behind
// the stop as well as ahead because a bus standing at a stop reports fixes on
// both sides of it.
const stoppedAtDistance = 30.0

// tripLocator places a driver vehicle on its GTFS trip with the rider engine's
// projection: how far along the trip's shape it is, and the stop it is at or
// heading to. It reads the schedule through a function, as gtfsCatalog does,
// so a refreshed schedule is the one it places vehicles on.
type tripLocator struct {
	index func() *rider.Index
	// maxShapeDistance is the rider engine's off-route distance
	// (RIDER_MAX_SHAPE_DISTANCE), the one the catalog hands the driver apps.
	maxShapeDistance float64
}

func newTripLocator(index func() *rider.Index, maxShapeDistance float64) *tripLocator {
	return &tripLocator{index: index, maxShapeDistance: maxShapeDistance}
}

// tripPlace is where a report put its vehicle on its trip.
type tripPlace struct {
	// alongShape is metres along the trip's shape: the projection hint for the
	// vehicle's next report on the same trip. It is nil until the vehicle has
	// been placed on that trip.
	alongShape   *float64
	stopID       string // "" when the report names no stop
	stopSequence int
	stoppedAt    bool
	// deviation is how far from its timetable the report put the vehicle
	// (scheduleDeviation), positive late; nil when that is not known.
	deviation *time.Duration
	// scheduledArrival is when the timetable has the trip reach stopID on the
	// run deviation was measured against; nil whenever deviation is.
	scheduledArrival *time.Time
}

// locate places a report on its trip, projecting it from hint, where the
// vehicle was last placed on the same trip. The hint keeps a loop or an
// out-and-back on the pass the vehicle is on rather than the nearest one. 0,
// the start of the shape, is the hint for a trip just begun: a loop passes its
// first stop again at the end, and the vehicle is at the start. With no hint
// the nearest pass wins: nothing is known about where the vehicle is.
//
// No stop is named when no schedule is loaded, the trip is not in it, the
// trip's shape was drawn through its stops (it cuts every corner between them,
// so the rider engine does not judge distance against it either), the report
// is further from the shape than the off-route distance, or the vehicle is
// past the last stop. In all but the last the report is not placed at all and
// the hint carries forward, so a detour does not cost the vehicle its pass.
//
// A placed report also says how far from its timetable it put the vehicle
// (scheduleDeviation), against the run runDayStart finds, and when that run is
// due at the stop. An unplaced one says nothing: a vehicle off its route is
// not early or late on it.
func (l *tripLocator) locate(loc *LocationReport, hint *float64) tripPlace {
	unplaced := tripPlace{alongShape: hint}
	ix := l.index()
	if ix == nil {
		return unplaced
	}
	trip, ok := ix.Trip(loc.TripID)
	if !ok || trip.ShapeFromStops {
		return unplaced
	}

	pos := rider.LatLon{Lat: loc.Latitude, Lon: loc.Longitude}
	proj := trip.Shape.Project(pos, hint)
	// The rider engine's off-route test, which the driver apps apply as well:
	// the shape distance plus the fix's own accuracy.
	tolerance := l.maxShapeDistance
	if loc.Accuracy != nil {
		tolerance += math.Max(*loc.Accuracy, 0)
	}
	if proj.DistanceToShape > tolerance {
		return unplaced
	}

	place := tripPlace{alongShape: &proj.AlongShape}
	along := stopSearchPosition(trip.Shape, pos, proj.AlongShape)
	// Looking from stoppedAtDistance back keeps a stop the vehicle has pulled
	// up just past as the one it is at, not the one after it.
	stop, named := rider.NextStop(trip, along-stoppedAtDistance)
	if named {
		place.stopID, place.stopSequence = stop.StopID, stop.Sequence
		place.stoppedAt = stop.AlongShape-along <= stoppedAtDistance
	}
	if dayStart, ok := runDayStart(ix, trip, loc); ok {
		// scheduleDeviation judges the vehicle by the same stop, so it is
		// named whenever the deviation is known.
		if d, ok := scheduleDeviation(trip, dayStart, along, time.Unix(loc.Timestamp, 0)); ok {
			arrival := dayStart.Add(stop.Arrival)
			place.deviation, place.scheduledArrival = &d, &arrival
		}
	}
	return place
}

// run describes the run loc's vehicle is on, by the loaded schedule
// (rider.TripInfo.Run): its start_time, the service date that counts from,
// and whether it is run by headway. A report without a start_date has its
// run counted from the service date its start falls in
// (rider.Index.RunServiceDate), since GTFS-RT needs both on a frequency run;
// when the trip does not run on that date the run gets neither, as a rider
// could not start a ride on it. A trip the schedule lacks has no start_time
// and is not run by headway.
func (l *tripLocator) run(loc *LocationReport) (startTime, startDate string, headway bool) {
	ix := l.index()
	if ix == nil {
		return "", "", false
	}
	trip, ok := ix.Trip(loc.TripID)
	if !ok {
		return "", "", false
	}
	startDate = loc.StartDate
	if startDate == "" && !loc.TripStartedAt.IsZero() {
		startDate = ix.RunServiceDate(trip, loc.TripStartedAt)
	}
	startTime, headway = trip.Run(startDate, ix.Timezone(), loc.TripStartedAt)
	if startTime == "" || (loc.StartDate == "" && !ix.ActiveOn(trip, startDate)) {
		return "", "", headway
	}
	return startTime, startDate, headway
}

// stopSearchPosition is where along shape to look for the stop of a fix at pos
// that projected to along. The projection clamps a fix beyond either end of
// the shape onto that end, which would make a vehicle parked 50 m past its
// terminal STOPPED_AT it. Such a fix is put as far past the end as it lies
// along the line the shape takes through the stoppedAtDistance metres nearest
// that end, so the radius means the same past a terminal as between stops.
func stopSearchPosition(shape *rider.ShapeGeom, pos rider.LatLon, along float64) float64 {
	switch {
	case along >= shape.Length-stoppedAtDistance:
		if past := distancePast(shape.PointAt(shape.Length-stoppedAtDistance), shape.PointAt(shape.Length), pos); past > 0 {
			return shape.Length + past
		}
	case along <= stoppedAtDistance:
		if before := distancePast(shape.PointAt(stoppedAtDistance), shape.PointAt(0), pos); before > 0 {
			return -before
		}
	}
	return along
}

// distancePast is how far pos lies beyond end on the line from from to end:
// positive past end, negative short of it.
func distancePast(from, end, pos rider.LatLon) float64 {
	angle := (rider.InitialBearing(end, pos) - rider.InitialBearing(from, end)) * math.Pi / 180
	return rider.Distance(end, pos) * math.Cos(angle)
}
