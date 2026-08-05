package org.onebusaway.vehicletracker.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import org.onebusaway.vehicletracker.MainActivity
import org.onebusaway.vehicletracker.R

const val NOTIFICATION_ID = 1001
private const val CHANNEL_ID = "tracking"

/**
 * Builds the ongoing tracking notification and its "tap to resume" degraded-path variant.
 * Creates the notification channel (minSdk 26 requirement, importance LOW) on first use.
 */
class TrackingNotification(private val context: Context) {

    init {
        ensureChannel()
    }

    private fun ensureChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            context.getString(R.string.tracking_notification_channel_name),
            NotificationManager.IMPORTANCE_LOW,
        )
        val manager = context.getSystemService(NotificationManager::class.java)
        manager?.createNotificationChannel(channel)
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

    private fun baseBuilder(): NotificationCompat.Builder =
        NotificationCompat.Builder(context, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_tracking_notification)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setContentIntent(contentIntent())

    /** The ongoing notification shown while location updates are actively being sent. */
    fun buildActiveNotification(statusText: String): Notification =
        baseBuilder()
            .setContentTitle(context.getString(R.string.tracking_notification_title))
            .setContentText(statusText)
            .build()

    /** Degraded-path variant shown when location updates stopped and need a foreground tap to resume. */
    fun buildResumeNotification(): Notification =
        baseBuilder()
            .setContentTitle(context.getString(R.string.tracking_notification_resume_title))
            .setContentText(context.getString(R.string.tracking_notification_resume_text))
            .build()
}
