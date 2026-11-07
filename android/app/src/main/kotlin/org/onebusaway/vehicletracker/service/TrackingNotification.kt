package org.onebusaway.vehicletracker.service

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import org.onebusaway.vehicletracker.MainActivity
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.ui.tracking.formatClock

const val NOTIFICATION_ID = 1001
private const val CHANNEL_ID = "tracking"
private const val ATTENTION_CHANNEL_ID = "tracking_attention"

/**
 * Builds the ongoing tracking notification and its "tap to resume" degraded-path variant, and posts them.
 * Creates their channels (a minSdk 26 requirement) on first use: the ongoing one's at importance LOW, the resume
 * one's at DEFAULT.
 */
class TrackingNotification(private val context: Context) {

    init {
        ensureChannels()
    }

    // Creating them again under the same ids renames them for drivers who already have them.
    private fun ensureChannels() {
        val manager = context.getSystemService(NotificationManager::class.java)
        manager?.createNotificationChannel(
            NotificationChannel(
                CHANNEL_ID,
                context.getString(R.string.tracking_notification_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            ),
        )
        // A channel of its own, because Android never changes a channel's importance once it exists: the resume
        // notification asks the driver to act, so it makes a sound, and the ongoing one stays quiet.
        manager?.createNotificationChannel(
            NotificationChannel(
                ATTENTION_CHANNEL_ID,
                context.getString(R.string.tracking_notification_attention_channel_name),
                NotificationManager.IMPORTANCE_DEFAULT,
            ),
        )
    }

    private fun contentIntent(): PendingIntent {
        val intent = Intent(context, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        return PendingIntent.getActivity(
            context,
            0,
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    private fun baseBuilder(channelId: String, priority: Int): NotificationCompat.Builder =
        NotificationCompat.Builder(context, channelId)
            .setSmallIcon(R.drawable.ic_tracking_notification)
            .setOnlyAlertOnce(true)
            .setPriority(priority)
            .setCategory(NotificationCompat.CATEGORY_LOCATION_SHARING)
            .setContentIntent(contentIntent())

    /**
     * The ongoing notification while reports are being sent: the route and its status as the screen's card says
     * them, over the next stop or what is wrong. [routeColor] tints it, as ARGB, when the schedule gives one.
     */
    internal fun buildActiveNotification(text: NotificationText, routeColor: Int?): Notification =
        baseBuilder(CHANNEL_ID, NotificationCompat.PRIORITY_LOW)
            .setOngoing(true)
            // Shown as soon as the trip starts, rather than after Android's usual few seconds' wait.
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .setContentTitle(title(text))
            .setContentText(line(text))
            .apply { routeColor?.let(::setColor) }
            .build()

    /**
     * Degraded-path variant shown when location updates stopped and need a foreground tap to resume. It sounds once:
     * posting it again while it is still showing stays silent.
     */
    fun buildResumeNotification(): Notification =
        baseBuilder(ATTENTION_CHANNEL_ID, NotificationCompat.PRIORITY_DEFAULT)
            .setContentTitle(context.getString(R.string.tracking_notification_resume_title))
            .setContentText(context.getString(R.string.tracking_notification_resume_text))
            .build()

    /**
     * Posts [notification] in the tracking notification's place. On Android 13+ a driver who denied notifications has
     * nothing to replace, and the foreground service keeps running without one, so the post is skipped rather than
     * handed to a call that requires POST_NOTIFICATIONS.
     */
    fun postIfPermitted(notification: Notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            return
        }
        NotificationManagerCompat.from(context).notify(NOTIFICATION_ID, notification)
    }

    private fun title(text: NotificationText): String {
        val status = when (val status = text.status) {
            is NotificationStatus.Minutes -> when {
                status.late > 0 -> context.resources.getQuantityString(R.plurals.tracking_adherence_minutes_late, status.late, status.late)
                status.late < 0 -> context.resources.getQuantityString(R.plurals.tracking_adherence_minutes_early, -status.late, -status.late)
                else -> context.getString(R.string.tracking_adherence_on_time)
            }
            NotificationStatus.OffRoute -> context.getString(R.string.tracking_adherence_off_route_short)
            NotificationStatus.WaitingForGps -> context.getString(R.string.tracking_adherence_waiting_for_gps)
            NotificationStatus.NoSchedule -> context.getString(R.string.tracking_adherence_no_schedule)
        }
        return text.route?.let { context.getString(R.string.tracking_notification_title, it, status) } ?: status
    }

    private fun line(text: NotificationText): String = when (text.problem) {
        TrackingProblem.NONE -> {
            val reporting = context.getString(R.string.tracking_reports_reporting)
            val stop = text.nextStop
            val zone = text.zone
            if (stop != null && zone != null) {
                context.getString(R.string.tracking_notification_next, stop.name, formatClock(stop.arrivalAt, zone), reporting)
            } else {
                reporting
            }
        }
        TrackingProblem.NO_NETWORK -> context.getString(R.string.tracking_problem_no_network)
        TrackingProblem.NO_GPS -> context.getString(R.string.tracking_problem_no_gps)
        TrackingProblem.CLOCK_SKEW -> context.getString(R.string.tracking_problem_clock_skew)
        TrackingProblem.AUTH_EXPIRED -> context.getString(R.string.tracking_notification_signed_out)
        TrackingProblem.REPORTS_REFUSED -> context.getString(R.string.tracking_notification_reports_refused)
    }
}
