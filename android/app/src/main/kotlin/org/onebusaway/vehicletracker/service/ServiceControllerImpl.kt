package org.onebusaway.vehicletracker.service

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat

/**
 * Real [ServiceController] backed by [LocationTrackingService]. [cancelTripNotifications] is
 * [TrackingNotification.cancel], which takes down both the ongoing notification and "Reports paused".
 */
class ServiceControllerImpl(
    private val context: Context,
    private val cancelTripNotifications: () -> Unit,
) : ServiceController {

    override fun startTracking() {
        ContextCompat.startForegroundService(context, Intent(context, LocationTrackingService::class.java))
    }

    override fun stopTracking() {
        // Every end of a trip comes here, and the service's stop does not take "Reports paused" down: RestartTracking
        // posts it with no service running, and the degraded path removes the ongoing notification with the foreground
        // status, then posts it under its own id, not as the service's notification.
        cancelTripNotifications()
        // Plain startService (not startForegroundService): stopTracking() is only ever called
        // while the app is in the foreground, so there's no need to (re-)arm the "must call
        // startForeground() within 5s" contract that startForegroundService imposes. This also
        // avoids that crash risk entirely if the service happened to have dropped out of the
        // foreground state (the degraded/no-background-permission path) at the moment the stop
        // intent is sent. With no service running (End Shift on the resume prompt), this starts
        // one just long enough to handle ACTION_STOP and stop itself.
        val intent = Intent(context, LocationTrackingService::class.java).apply {
            action = LocationTrackingService.ACTION_STOP
        }
        context.startService(intent)
    }
}
