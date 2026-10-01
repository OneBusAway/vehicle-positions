package org.onebusaway.vehicletracker.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import java.time.Duration

/** `TestScheduledOffsetAt` (`rider/index_test.go`), on T1's absolute times. */
class ScheduleInterpolatorTest {
    private val stops = TripFixtures.t1.stops

    @Test fun `before the first stop is the first stop's arrival`() {
        assertEquals(at(8, 0), ScheduleInterpolator.scheduledTime(-10.0, stops))
    }

    @Test fun `between stops is interpolated from departure to arrival`() {
        val mid = requireNotNull(ScheduleInterpolator.scheduledTime(250.0, stops))
        assertEquals(0.0, Duration.between(at(8, 2, 30), mid).seconds.toDouble(), 3.0)
    }

    @Test fun `beyond the last stop is the last stop's arrival`() {
        assertEquals(at(8, 10), ScheduleInterpolator.scheduledTime(5000.0, stops))
    }

    @Test fun `no stops has no scheduled time`() {
        assertNull(ScheduleInterpolator.scheduledTime(100.0, emptyList()))
    }

    @Test fun `a dwell is measured from the departure, not the arrival`() {
        // ST2 arrives 08:05 and leaves 08:07: a vehicle 250 m past it is due halfway from 08:07 to
        // ST3's 08:10, not halfway from 08:05.
        val dwelling = stops.toMutableList().apply { this[1] = this[1].copy(departureAt = at(8, 7)) }
        val due = requireNotNull(ScheduleInterpolator.scheduledTime(750.5, dwelling))
        assertEquals(0.0, Duration.between(at(8, 8, 30), due).seconds.toDouble(), 1.0)
    }

    @Test fun `stops at the same distance interpolate from the later one's departure`() {
        // Two stop times at one point on the shape (a timepoint listed twice) must not divide by a
        // zero span: the vehicle past them is due between the second one's departure and ST3.
        val doubled = listOf(
            stops[0],
            stops[1],
            stops[1].copy(id = "ST2b", arrivalAt = at(8, 6), departureAt = at(8, 6)),
            stops[2],
        )
        val due = requireNotNull(ScheduleInterpolator.scheduledTime(750.5, doubled))
        assertEquals(0.0, Duration.between(at(8, 8), due).seconds.toDouble(), 1.0)
    }
}
