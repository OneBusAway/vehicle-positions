package org.onebusaway.vehicletracker.service

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.engine.TripFixtures

/** When the notification's line says the reports waiting, as the status card's does. */
class NotificationWaitingTest {
    private val trip = ActiveTrip(7L, "T1", "4317", "100263", "20260902", 100L)
    private val waiting = ReportsWaiting(37, WaitingOutlook.WILL_SEND)

    private fun said(problem: TrackingProblem, waiting: ReportsWaiting?) =
        notificationText(TrackingState(active = true, problem = problem, geometry = TripFixtures.t1), trip, waiting)

    @Test fun `it takes the next stop's place, and No connection's`() {
        assertEquals(waiting, said(TrackingProblem.NONE, waiting).waiting)
        assertEquals(waiting, said(TrackingProblem.NO_NETWORK, waiting).waiting)
        assertEquals(ReportsWaiting(3, WaitingOutlook.NEVER), said(TrackingProblem.NO_NETWORK, ReportsWaiting(3, WaitingOutlook.NEVER)).waiting)
    }

    @Test fun `any other problem keeps the line`() {
        val others = TrackingProblem.entries - setOf(TrackingProblem.NONE, TrackingProblem.NO_NETWORK)
        for (problem in others) {
            assertNull("$problem", said(problem, waiting).waiting)
        }
    }

    // The service posts the notification only when what it says changes.
    @Test fun `it is said again as the count changes, and not while a problem keeps it off the line`() {
        assertNotEquals(said(TrackingProblem.NO_NETWORK, waiting), said(TrackingProblem.NO_NETWORK, waiting.copy(count = 38)))
        assertNotEquals(said(TrackingProblem.NONE, waiting), said(TrackingProblem.NONE, null))
        assertEquals(said(TrackingProblem.NO_GPS, waiting), said(TrackingProblem.NO_GPS, waiting.copy(count = 38)))
    }
}
