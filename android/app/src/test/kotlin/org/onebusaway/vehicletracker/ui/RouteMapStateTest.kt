package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.engine.Adherence
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.ShapeGeometry
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.ui.map.FollowHeading
import org.onebusaway.vehicletracker.ui.map.routeMapState

class RouteMapStateTest {
    private val t1 = TripFixtures.t1
    private val t1Shape = requireNotNull(ShapeGeometry.of(t1.shapePoints))

    private fun judge(trip: TripGeometry, fix: LocationFix): Adherence =
        requireNotNull(AdherenceEvaluator.of(trip)).evaluate(fix, previous = null)

    @Test fun `on the route the vehicle is at its fix and nothing is snapped`() {
        val adherence = judge(t1, fix(47.6020, -122.3300, at(8, 2)))

        val state = routeMapState(t1, t1Shape, adherence, FollowHeading())

        assertTrue(state.isOnRoute)
        assertEquals(GeoPoint(47.6020, -122.3300), state.vehicle)
        assertNull(state.snapped)
        assertEquals("ST2 is next", 1, state.nextStopIndex)
    }

    @Test fun `off the route the snapped point is where the shape was matched`() {
        // About 225 m east of the shape, level with ST2: well past the 60 m threshold.
        val adherence = judge(t1, fix(47.6045, -122.3270, at(8, 5)))

        val state = routeMapState(t1, t1Shape, adherence, FollowHeading())

        assertFalse(state.isOnRoute)
        assertEquals("the vehicle stays at its fix", GeoPoint(47.6045, -122.3270), state.vehicle)
        val snapped = requireNotNull(state.snapped)
        assertEquals(47.6045, snapped.lat, 1e-4)
        assertEquals(-122.3300, snapped.lon, 1e-4)
    }

    @Test fun `a fix with no course keeps the heading of the one before`() {
        val heading = FollowHeading()
        val moving = judge(t1, fix(47.6020, -122.3300, at(8, 2)).copy(bearing = 90.0))
        val stopped = judge(t1, fix(47.6021, -122.3300, at(8, 3)).copy(bearing = null))

        assertEquals(90.0, routeMapState(t1, t1Shape, moving, heading).course, 0.0)
        assertEquals(90.0, routeMapState(t1, t1Shape, stopped, heading).course, 0.0)
    }

    /** A loop that calls at stop A when it sets off and again when it gets back. */
    @Test fun `the next stop is the right call at a stop the trip visits twice`() {
        fun stopA(alongShapeM: Double, minute: Int) =
            TripStop("A", "Stop A", 47.6000, -122.3300, alongShapeM, arrivalAt = at(9, minute), departureAt = at(9, minute))
        val loop = TripFixtures.loop.copy(stops = listOf(stopA(0.0, 0), stopA(1995.0, 20)))
        val shape = requireNotNull(ShapeGeometry.of(loop.shapePoints))
        // Halfway round: the first call at A is behind, the second is ahead.
        val adherence = judge(loop, fix(47.6045, -122.3267, at(9, 10)))

        val state = routeMapState(loop, shape, adherence, FollowHeading())

        assertEquals(1, state.nextStopIndex)
    }
}
