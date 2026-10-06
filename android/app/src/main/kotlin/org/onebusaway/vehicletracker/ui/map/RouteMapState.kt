package org.onebusaway.vehicletracker.ui.map

import org.onebusaway.vehicletracker.engine.Adherence
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.ShapeGeometry
import org.onebusaway.vehicletracker.engine.TripGeometry

/** What the map shows for one fix: the decisions iOS's `RouteMapViewController.update` makes. */
data class RouteMapState(
    val vehicle: GeoPoint,
    /** Degrees clockwise from north. */
    val course: Double,
    val isOnRoute: Boolean,
    /** Where on the shape the vehicle was matched to. Drawn only while it is off the route. */
    val snapped: GeoPoint?,
    /** The next stop's place in the trip's stop list. */
    val nextStopIndex: Int,
)

/**
 * The map state for [adherence]. Call it once for each new fix: it moves [heading] on.
 *
 * The next stop is found by its place in the list, not by its id. A loop calls at the same stop
 * twice, and by id the first call would be marked for the whole trip. iOS matches id and
 * sequence for the same reason; here the two calls differ in distance along the shape and in
 * time, which is enough to tell them apart.
 */
fun routeMapState(
    geometry: TripGeometry,
    shape: ShapeGeometry,
    adherence: Adherence,
    heading: FollowHeading,
): RouteMapState = RouteMapState(
    vehicle = GeoPoint(adherence.fix.latitude, adherence.fix.longitude),
    course = heading.update(adherence.fix.bearing),
    isOnRoute = adherence.isOnRoute,
    snapped = if (adherence.isOnRoute) null else shape.pointAt(adherence.projection.alongShape),
    nextStopIndex = geometry.stops.indexOf(adherence.nextStop),
)
