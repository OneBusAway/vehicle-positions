package org.onebusaway.vehicletracker.service

import android.app.Notification
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.WaitingOutlook

/** The tracking notification's line while reports wait on the phone: the status card's waiting line, in its words. */
@RunWith(AndroidJUnit4::class)
class TrackingNotificationWaitingTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val notification = TrackingNotification(context)

    private fun line(problem: TrackingProblem, waiting: ReportsWaiting?): String {
        val text = NotificationText("7", NotificationStatus.Minutes(0), nextStop = null, zone = null, problem, waiting)
        return notification.buildActiveNotification(text, routeColor = null).extras.getCharSequence(Notification.EXTRA_TEXT).toString()
    }

    @Test
    fun itSaysHowManyAreWaiting_andThatTheyWillSend() {
        assertEquals("1 report to send when online", line(TrackingProblem.NO_NETWORK, ReportsWaiting(1, WaitingOutlook.WILL_SEND)))
        assertEquals("37 reports to send when online", line(TrackingProblem.NONE, ReportsWaiting(37, WaitingOutlook.WILL_SEND)))
        // The phone keeps up to 8,640, so the count is grouped as the phone's language groups it: 1,244 in English, 1.244
        // in German.
        val grouped = String.format(context.resources.configuration.locales[0], "%,d", 1244)
        assertEquals("$grouped reports to send when online", line(TrackingProblem.NO_NETWORK, ReportsWaiting(1244, WaitingOutlook.WILL_SEND)))
    }

    @Test
    fun reportsThatWillNeverSend_sayThat() {
        assertEquals("37 reports won't send", line(TrackingProblem.NO_NETWORK, ReportsWaiting(37, WaitingOutlook.NEVER)))
    }

    @Test
    fun withNothingWaiting_itSaysWhatItDidBefore() {
        assertEquals("No connection", line(TrackingProblem.NO_NETWORK, waiting = null))
        assertEquals("Reporting", line(TrackingProblem.NONE, waiting = null))
    }
}
