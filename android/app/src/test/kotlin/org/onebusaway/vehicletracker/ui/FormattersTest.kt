package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import org.onebusaway.vehicletracker.ui.tracking.adherenceTone
import org.onebusaway.vehicletracker.ui.tracking.deviationMinutes
import org.onebusaway.vehicletracker.ui.tracking.formatClock
import org.onebusaway.vehicletracker.ui.tracking.formatDuration
import org.onebusaway.vehicletracker.ui.tracking.vehicleTone
import java.time.ZoneId
import java.util.Locale

/**
 * The arithmetic behind the tracking screen's words, after iOS's `FormattersTests`. The words
 * themselves are string resources, which `ScreenFlowTest` reads on a device.
 */
class FormattersTest {
    private val evaluator = requireNotNull(AdherenceEvaluator.of(TripFixtures.t1))

    /** ST1's scheduled time is exactly 08:00, so the status follows from [secondsLate] alone. */
    private fun atStop1(secondsLate: Long) =
        evaluator.evaluate(fix(47.6000, -122.3300, at(8, 0).plusSeconds(secondsLate)), previous = null)

    @Test fun `deviation is whole minutes, and none within a minute either way`() {
        assertEquals(0, deviationMinutes(0.0))
        assertEquals(0, deviationMinutes(59.0))
        assertEquals(0, deviationMinutes(-59.0))
        assertEquals(1, deviationMinutes(60.0))
        assertEquals(3, deviationMinutes(200.0))
        assertEquals(-2, deviationMinutes(-90.0))
        assertEquals(60, deviationMinutes(3600.0))
    }

    @Test fun `clock times are read in the agency's zone, not the device's`() {
        // 08:05 in Los Angeles is 18:05 in Nairobi; only the zone argument decides which is shown.
        val stopTime = at(8, 5)
        assertTrue(formatClock(stopTime, TripFixtures.pacific, Locale.US).startsWith("8:05"))
        assertTrue(formatClock(stopTime, ZoneId.of("Africa/Nairobi"), Locale.US).startsWith("6:05"))
    }

    @Test fun `trip duration counts minutes, then hours`() {
        assertEquals("00:00", formatDuration(0))
        assertEquals("01:05", formatDuration(65))
        assertEquals("1:02:05", formatDuration(3725))
        assertEquals("a clock behind the trip's start shows no time, not a negative one", "00:00", formatDuration(-5))
    }

    @Test fun `tones follow OneBusAway's convention`() {
        assertEquals(StatusTone.ON_TIME, adherenceTone(atStop1(0)))
        assertEquals(StatusTone.EARLY, adherenceTone(atStop1(-120)))
        assertEquals(StatusTone.LATE, adherenceTone(atStop1(400)))

        val offRoute = evaluator.evaluate(fix(47.6045, -122.3200, at(8, 5)), previous = null)
        assertEquals(AdherenceStatus.OFF_ROUTE, offRoute.status)
        assertEquals(StatusTone.NEUTRAL, adherenceTone(offRoute))
    }

    @Test fun `the vehicle's ring takes its tone, and is grey before the first fix`() {
        assertEquals(StatusTone.NEUTRAL, vehicleTone(null))
        assertEquals(StatusTone.ON_TIME, vehicleTone(atStop1(0)))
        assertEquals(StatusTone.LATE, vehicleTone(atStop1(400)))
        assertEquals(StatusTone.EARLY, vehicleTone(atStop1(-1000)))
        assertEquals(StatusTone.NEUTRAL, vehicleTone(evaluator.evaluate(fix(47.6045, -122.3200, at(8, 5)), previous = null)))
    }

    @Test fun `off schedule keeps the tone of its direction`() {
        val farAhead = atStop1(-1000)
        val farBehind = atStop1(6000)
        assertEquals(AdherenceStatus.OFF_SCHEDULE, farAhead.status)
        assertEquals(AdherenceStatus.OFF_SCHEDULE, farBehind.status)

        assertEquals(StatusTone.EARLY, adherenceTone(farAhead))
        assertEquals(StatusTone.LATE, adherenceTone(farBehind))
    }
}
