package main

import (
	"math"

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
	// vehicle's next report on the same trip.
	alongShape   float64
	stopID       string // "" when the report names no stop
	stopSequence int
	stoppedAt    bool
}

// locate places a report on its trip, projecting it from hint, where the
// vehicle was last placed on the same trip. The hint keeps a loop or an
// out-and-back on the pass the vehicle is on rather than the nearest one. 0,
// the start of the shape, is the hint for a trip just begun: a loop passes its
// first stop again at the end, and the vehicle is at the start.
//
// No stop is named when no schedule is loaded, the trip is not in it, the
// trip's shape was drawn through its stops (it cuts every corner between them,
// so the rider engine does not judge distance against it either), the report
// is further from the shape than the off-route distance, or the vehicle is
// past the last stop. In all but the last the report is not placed at all and
// the hint carries forward, so a detour does not cost the vehicle its pass.
func (l *tripLocator) locate(loc *LocationReport, hint float64) tripPlace {
	unplaced := tripPlace{alongShape: hint}
	ix := l.index()
	if ix == nil {
		return unplaced
	}
	trip, ok := ix.Trip(loc.TripID)
	if !ok || trip.ShapeFromStops {
		return unplaced
	}

	proj := trip.Shape.Project(rider.LatLon{Lat: loc.Latitude, Lon: loc.Longitude}, &hint)
	// The rider engine's off-route test, which the driver apps apply as well:
	// the shape distance plus the fix's own accuracy.
	tolerance := l.maxShapeDistance
	if loc.Accuracy != nil {
		tolerance += math.Max(*loc.Accuracy, 0)
	}
	if proj.DistanceToShape > tolerance {
		return unplaced
	}

	place := tripPlace{alongShape: proj.AlongShape}
	// Looking from stoppedAtDistance back keeps a stop the vehicle has pulled
	// up just past as the one it is at, not the one after it.
	if stop, ok := rider.NextStop(trip, proj.AlongShape-stoppedAtDistance); ok {
		place.stopID, place.stopSequence = stop.StopID, stop.Sequence
		place.stoppedAt = stop.AlongShape-proj.AlongShape <= stoppedAtDistance
	}
	return place
}
