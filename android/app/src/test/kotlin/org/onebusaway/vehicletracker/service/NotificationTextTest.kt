package org.onebusaway.vehicletracker.service

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix

class NotificationTextTest {
    private val t1 = TripFixtures.t1.copy(routeShortName = "7")
    private val trip = ActiveTrip(7L, "T1", "4317", "100263", "20260902", 100L)
    private val evaluator = requireNotNull(AdherenceEvaluator.of(t1))

    /** At ST1, scheduled at exactly 08:00, so the status follows from [secondsLate] alone. */
    private fun atStop1(secondsLate: Long, problem: TrackingProblem = TrackingProblem.NONE) = TrackingState(
        active = true,
        problem = problem,
        geometry = t1,
        adherence = evaluator.evaluate(fix(47.6000, -122.3300, at(8, 0).plusSeconds(secondsLate)), previous = null),
    )

    @Test fun `on time, late and early are the card's whole minutes, with the route and the next stop`() {
        for ((secondsLate, minutes) in listOf(0L to 0, 400L to 7, -120L to -2, 6000L to 100)) {
            assertEquals(
                "$secondsLate s late",
                NotificationText("7", NotificationStatus.Minutes(minutes), t1.stops[1], t1.timezone, TrackingProblem.NONE),
                notificationText(atStop1(secondsLate), trip),
            )
        }
    }

    @Test fun `off route leaves out the distance, which changes with every fix`() {
        val near = evaluator.evaluate(fix(47.6045, -122.3200, at(8, 5)), previous = null)
        val far = evaluator.evaluate(fix(47.6045, -122.3100, at(8, 5)), previous = null)

        val text = notificationText(TrackingState(active = true, geometry = t1, adherence = near), trip)

        assertEquals(NotificationStatus.OffRoute, text.status)
        assertEquals(near.nextStop, text.nextStop)
        assertEquals(text, notificationText(TrackingState(active = true, geometry = t1, adherence = far), trip))
    }

    @Test fun `before the first fix, and with no schedule, there is no next stop to name`() {
        val waiting = notificationText(TrackingState(active = true, geometry = t1), trip)
        assertEquals(NotificationStatus.WaitingForGps, waiting.status)
        assertNull(waiting.nextStop)

        val noSchedule = notificationText(TrackingState(active = true, geometry = null), trip)
        assertEquals(NotificationStatus.NoSchedule, noSchedule.status)
        assertNull(noSchedule.nextStop)
    }

    @Test fun `a problem takes the reporting line's place`() {
        for (problem in TrackingProblem.entries) {
            assertEquals(problem, notificationText(atStop1(0, problem), trip).problem)
        }
    }

    @Test fun `the route is its short name, or its id without one, and none before the trip is read`() {
        assertEquals("7", notificationText(atStop1(0), trip).route)
        assertEquals("100263", notificationText(TrackingState(active = true, geometry = TripFixtures.t1), trip).route)
        assertEquals("100263", notificationText(TrackingState(active = true, geometry = null), trip).route)
        assertNull(notificationText(TrackingState(), null).route)
    }

    @Test fun `a report sent, or the trip clock, does not change what it says`() {
        val before = atStop1(0)
        assertEquals(notificationText(before, trip), notificationText(before.copy(fixesSent = 41), trip))
        assertNotEquals(notificationText(before, trip), notificationText(before.copy(problem = TrackingProblem.NO_GPS), trip))
    }
}
