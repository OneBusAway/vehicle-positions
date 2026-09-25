package org.onebusaway.vehicletracker.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix

/** `AdherenceEvaluatorTests.swift`'s nine cases, by name, plus the server window's own. */
class AdherenceEvaluatorTest {
    private val evaluator = requireNotNull(AdherenceEvaluator.of(TripFixtures.t1))

    /** ST2's coordinates project ~0.4 m past its declared 500 m, so its schedule is a fraction of a second into the next leg. */
    private fun statusAtStop2(secondsLate: Long): AdherenceStatus =
        evaluator.evaluate(fix(47.6045, -122.3300, at(8, 5).plusSeconds(secondsLate)), previous = null).status

    /** ST1 projects to exactly 0 m, so its scheduled time is exactly 08:00: the place to test a boundary. */
    private fun statusAtStop1(secondsLate: Long, on: AdherenceEvaluator = evaluator): AdherenceStatus =
        on.evaluate(fix(47.6000, -122.3300, at(8, 0).plusSeconds(secondsLate)), previous = null).status

    @Test fun `on time at a stop`() {
        val a = evaluator.evaluate(fix(47.6045, -122.3300, at(8, 5)), previous = null)
        assertTrue(a.isOnRoute)
        assertEquals(500.0, a.projection.alongShape, 3.0)
        assertEquals(0.0, a.scheduleDeviationSec, 3.0)
        assertEquals(AdherenceStatus.ON_TIME, a.status)
        assertEquals("ST3", a.nextStop.id)
        assertEquals(501.0, a.distanceToNextStopM, 5.0)
        assertEquals(501.0 / 8, a.timeToNextStopSec, 1.0)
    }

    @Test fun `late and early thresholds`() {
        assertEquals("4 min late is still on time", AdherenceStatus.ON_TIME, statusAtStop2(240))
        assertEquals(AdherenceStatus.LATE, statusAtStop2(301))
        assertEquals(AdherenceStatus.ON_TIME, statusAtStop2(-59))
        assertEquals(AdherenceStatus.EARLY, statusAtStop2(-61))
        assertEquals("beyond the server's late window", AdherenceStatus.OFF_SCHEDULE, statusAtStop2(5401))
        assertEquals("beyond the server's early window", AdherenceStatus.OFF_SCHEDULE, statusAtStop2(-901))

        // Exactly at each display cut-off, and one second past it.
        assertEquals(AdherenceStatus.ON_TIME, statusAtStop1(-60))
        assertEquals(AdherenceStatus.EARLY, statusAtStop1(-61))
        assertEquals(AdherenceStatus.ON_TIME, statusAtStop1(300))
        assertEquals(AdherenceStatus.LATE, statusAtStop1(301))
    }

    @Test fun `off schedule uses the server's window`() {
        // Exactly at each edge of the server's window, and one second past it.
        assertEquals(AdherenceStatus.EARLY, statusAtStop1(-900))
        assertEquals(AdherenceStatus.OFF_SCHEDULE, statusAtStop1(-901))
        assertEquals(AdherenceStatus.LATE, statusAtStop1(5400))
        assertEquals(AdherenceStatus.OFF_SCHEDULE, statusAtStop1(5401))

        // A server window tighter than the display cut-offs still wins: 45 s early is inside the
        // app's minute, but outside the server's 30 s.
        val strict = requireNotNull(
            AdherenceEvaluator.of(TripFixtures.t1.copy(thresholds = AdherenceThresholds(60.0, scheduleEarlyS = 30, scheduleLateS = 120))),
        )
        assertEquals(AdherenceStatus.OFF_SCHEDULE, statusAtStop1(-45, on = strict))
        assertEquals(AdherenceStatus.ON_TIME, statusAtStop1(120, on = strict))
        assertEquals(AdherenceStatus.OFF_SCHEDULE, statusAtStop1(121, on = strict))
    }

    @Test fun `off route uses threshold plus accuracy`() {
        // ~75 m east of the line: beyond 60 m with 5 m accuracy, within it with 20 m accuracy.
        val coarse = evaluator.evaluate(fix(47.60225, -122.3290, at(8, 2, 30), accuracy = 20.0), previous = null)
        assertTrue(coarse.isOnRoute)
        assertEquals(AdherenceStatus.ON_TIME, coarse.status)
        val precise = evaluator.evaluate(fix(47.60225, -122.3290, at(8, 2, 30), accuracy = 5.0), previous = null)
        assertFalse(precise.isOnRoute)
        assertEquals(AdherenceStatus.OFF_ROUTE, precise.status)
        assertEquals(75.0, precise.projection.distanceToShape, 3.0)
    }

    @Test fun `unknown accuracy adds nothing`() {
        val unknown = evaluator.evaluate(fix(47.60225, -122.3290, at(8, 2, 30), accuracy = null), previous = null)
        assertFalse(unknown.isOnRoute)
        val negative = evaluator.evaluate(fix(47.60225, -122.3290, at(8, 2, 30), accuracy = -1.0), previous = null)
        assertFalse(negative.isOnRoute)
    }

    @Test fun `slow or unknown speed uses the floor`() {
        val unknown = evaluator.evaluate(fix(47.6045, -122.3300, at(8, 5), speed = null), previous = null)
        assertEquals(501.0 / 3, unknown.timeToNextStopSec, 1.0)
        val crawling = evaluator.evaluate(fix(47.6045, -122.3300, at(8, 5), speed = 1.0), previous = null)
        assertEquals(501.0 / 3, crawling.timeToNextStopSec, 1.0)
    }

    @Test fun `last stop is next beyond the end`() {
        val a = evaluator.evaluate(fix(47.6095, -122.3300, at(8, 11)), previous = null)
        assertEquals("ST3", a.nextStop.id)
        // The fixture's stop is declared at 1001 m but the shape's own haversine length over its
        // three points is ~1000.75 m, so the projection clamps just short of the declared stop.
        assertTrue(a.distanceToNextStopM <= 1)
    }

    @Test fun `previous on-route fix supplies the hint`() {
        val ev = requireNotNull(AdherenceEvaluator.of(TripFixtures.loop))
        val lateInLoop = ev.evaluate(fix(47.6001, -122.3292, at(9, 15)), previous = null) // on the last leg, 11 m off it
        assertTrue(lateInLoop.isOnRoute)
        assertTrue(lateInLoop.projection.alongShape > 1400)

        val atShared = ev.evaluate(fix(47.6000, -122.3300, at(9, 20)), previous = lateInLoop)
        assertTrue("the hint picks the last pass", atShared.projection.alongShape > ev.shape.length - 60)
        val unhinted = ev.evaluate(fix(47.6000, -122.3300, at(9, 20)), previous = null)
        assertTrue(unhinted.projection.alongShape < 1)
    }

    @Test fun `off-route previous does not hint`() {
        // On a straight shape a hint cannot change the answer, so this uses the loop, where the
        // shared start/end point is told apart by the hint alone.
        val ev = requireNotNull(AdherenceEvaluator.of(TripFixtures.loop))
        val off = ev.evaluate(fix(47.5990, -122.3292, at(9, 15)), previous = null) // 111 m south of the last leg
        assertFalse(off.isOnRoute)
        assertTrue("its projection would pick the last pass, were it used", off.projection.alongShape > 1400)

        val back = ev.evaluate(fix(47.6000, -122.3300, at(9, 16)), previous = off)
        assertTrue(back.isOnRoute)
        assertTrue("no hint, so the first pass wins", back.projection.alongShape < 1)
    }

    @Test fun `rejects a trip without a shape or stops`() {
        assertNull(AdherenceEvaluator.of(TripFixtures.t1.copy(shapePoints = listOf(GeoPoint(47.6, -122.33)))))
        assertNull(AdherenceEvaluator.of(TripFixtures.t1.copy(shapePoints = emptyList())))
        assertNull(AdherenceEvaluator.of(TripFixtures.t1.copy(stops = emptyList())))
    }
}
