package org.onebusaway.vehicletracker.service

import android.app.Notification
import android.app.NotificationManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.data.TrackingProblem

/**
 * Covers what the JVM cannot: the channels Android creates, which one each notification goes out on, and the flags it
 * is built with. The channel ids are spelled out because Android keeps them on the phone with the driver's settings,
 * so renaming one is a change.
 */
@RunWith(AndroidJUnit4::class)
class TrackingNotificationTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val notification = TrackingNotification(context)
    private val manager = context.getSystemService(NotificationManager::class.java)

    @Test
    fun theResumeNotificationGoesOutOnTheChannelThatSounds() {
        assertEquals("tracking_attention", notification.buildResumeNotification().channelId)
        assertEquals(NotificationManager.IMPORTANCE_DEFAULT, manager.getNotificationChannel("tracking_attention").importance)
    }

    @Test
    fun aTapTakesTheResumeNotificationDown() {
        val flags = notification.buildResumeNotification().flags

        assertEquals(Notification.FLAG_AUTO_CANCEL, flags and Notification.FLAG_AUTO_CANCEL)
    }

    @Test
    fun theOngoingNotificationStaysOnTheQuietChannel() {
        val text = NotificationText(
            route = "7",
            status = NotificationStatus.WaitingForGps,
            nextStop = null,
            zone = null,
            problem = TrackingProblem.NONE,
        )

        assertEquals("tracking", notification.buildActiveNotification(text, routeColor = null).channelId)
        assertEquals(NotificationManager.IMPORTANCE_LOW, manager.getNotificationChannel("tracking").importance)
    }
}
