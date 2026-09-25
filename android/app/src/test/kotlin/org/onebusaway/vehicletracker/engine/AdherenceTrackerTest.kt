package org.onebusaway.vehicletracker.engine

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

    @Test fun `an off-route fix does not advance the hint`() {
        val tracker = AdherenceTracker(TripFixtures.loop)
        val off = requireNotNull(tracker.onFix(fix(47.5990, -122.3292, at(9, 15))))
        assertTrue("its projection would pick the last pass, were it used", off.projection.alongShape > 1400)

        val atShared = requireNotNull(tracker.onFix(shared))

        assertTrue("no hint, so the first pass wins", atShared.projection.alongShape < 1)
    }

    @Test fun `a trip with nothing to judge against is judged not at all`() {
        assertNull(AdherenceTracker(null).onFix(shared))
        assertNull(AdherenceTracker(TripFixtures.loop.copy(stops = emptyList())).onFix(shared))
    }
}
