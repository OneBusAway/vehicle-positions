package org.onebusaway.vehicletracker.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix

/** The loop's shared start/end point is told apart by the hint alone, so it shows what the tracker hands on. */
class AdherenceTrackerTest {
    private val loopLength = requireNotNull(ShapeGeometry.of(TripFixtures.loop.shapePoints)).length
    private val shared = fix(47.6000, -122.3300, at(9, 20))

    @Test fun `an on-route fix supplies the hint for the next`() {
        val tracker = AdherenceTracker(TripFixtures.loop)
        val lateInLoop = requireNotNull(tracker.onFix(fix(47.6001, -122.3292, at(9, 15))))
        assertTrue(lateInLoop.isOnRoute)

        val atShared = requireNotNull(tracker.onFix(shared))

        assertTrue("the previous fix's hint picks the last pass", atShared.projection.alongShape > loopLength - 60)
    }

    @Test fun `an off-route fix keeps the last on-route hint`() {
        val tracker = AdherenceTracker(TripFixtures.loop)
        val lateInLoop = requireNotNull(tracker.onFix(fix(47.6001, -122.3292, at(9, 15))))
        assertTrue(lateInLoop.isOnRoute)
        // A detour 112 m west of the first leg: off route, and its own projection is back on the
        // first pass — the position the next fix must not be judged against.
        val detour = requireNotNull(tracker.onFix(fix(47.6040, -122.3315, at(9, 17))))
        assertEquals(AdherenceStatus.OFF_ROUTE, detour.status)
        assertTrue(detour.projection.alongShape < 600)

        val rejoined = requireNotNull(tracker.onFix(shared))

        assertTrue("the hint from before the detour still picks the last pass", rejoined.projection.alongShape > loopLength - 60)
        // On the first pass the bus would read 20 minutes late for the rest of the trip.
        assertEquals(AdherenceStatus.ON_TIME, rejoined.status)
    }

    @Test fun `a trip with nothing to judge against is judged not at all`() {
        assertNull(AdherenceTracker(null).onFix(shared))
        assertNull(AdherenceTracker(TripFixtures.loop.copy(stops = emptyList())).onFix(shared))
    }
}
