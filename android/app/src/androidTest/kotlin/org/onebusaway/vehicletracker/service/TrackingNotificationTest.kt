package org.onebusaway.vehicletracker.service

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.os.Build
import android.os.SystemClock
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.data.TrackingProblem

/**
 * Covers what the JVM cannot: the channels Android creates, which one each notification goes out on, the flags it is
 * built with, and the ids it is posted and cancelled under. The channel ids are spelled out because Android keeps them
 * on the phone with the driver's settings, so renaming one is a change.
 */
@RunWith(AndroidJUnit4::class)
class TrackingNotificationTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val notification = TrackingNotification(context)
    private val manager = context.getSystemService(NotificationManager::class.java)

    private val activeText = NotificationText(
        route = "7",
        status = NotificationStatus.WaitingForGps,
        nextStop = null,
        zone = null,
        problem = TrackingProblem.NONE,
    )
    private val both = setOf("tracking", "tracking_attention")

    @Before
    fun allowNotifications() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            InstrumentationRegistry.getInstrumentation().uiAutomation
                .grantRuntimePermission(context.packageName, Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    @After
    fun takeNotificationsDown() {
        notification.cancel()
    }

    /**
     * The channels of the notifications showing, once Android has handled every post and cancel, which it does off this
     * thread. Channels rather than ids, so the check does not lean on the id constants it is about.
     */
    private fun showingChannels(expected: Set<String>): Set<String> {
        val deadline = SystemClock.uptimeMillis() + 5_000
        var channels = manager.activeNotifications.map { it.notification.channelId }.toSet()
        while (channels != expected && SystemClock.uptimeMillis() < deadline) {
            SystemClock.sleep(50)
            channels = manager.activeNotifications.map { it.notification.channelId }.toSet()
        }
        return channels
    }

    private fun postBoth() {
        notification.postActiveIfPermitted(notification.buildActiveNotification(activeText, routeColor = null))
        notification.postResumeIfPermitted()
    }

    @Test
    fun reportsPausedIsANewNotificationBesideTheOngoingOne() {
        postBoth()

        // Under the ongoing one's id it would replace it, as an update that stays silent.
        assertEquals(both, showingChannels(both))
    }

    @Test
    fun aTripEndTakesBothDown() {
        postBoth()
        assertEquals(both, showingChannels(both))

        notification.cancel()

        assertEquals(emptySet<String>(), showingChannels(emptySet()))
    }

    @Test
    fun reportingAgainTakesOnlyReportsPausedDown() {
        postBoth()
        assertEquals(both, showingChannels(both))

        notification.cancelResume()

        assertEquals(setOf("tracking"), showingChannels(setOf("tracking")))
    }

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
        assertEquals("tracking", notification.buildActiveNotification(activeText, routeColor = null).channelId)
        assertEquals(NotificationManager.IMPORTANCE_LOW, manager.getNotificationChannel("tracking").importance)
    }
}
