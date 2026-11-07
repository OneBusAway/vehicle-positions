package org.onebusaway.vehicletracker.service

import android.util.Log
import kotlinx.coroutines.flow.first
import org.onebusaway.vehicletracker.data.TripStateStore
import java.time.Duration

private const val TAG = "RestartTracking"

/**
 * A trip started this long ago or more is not restarted by itself. No run lasts that long, so the trip was most likely
 * left open, and restarting it would put the phone's position on the public feed without anyone having asked.
 */
internal val MAX_RESTART_AGE: Duration = Duration.ofHours(12)

/** What [RestartTracking.restart] did with the trip stored on the phone. */
enum class RestartOutcome { NoTrip, Started, AskedDriver, AppInFront }

/** Asks the driver to open the app, which shows Resume, for a trip whose reports did not start again by themselves. */
fun interface ResumeNotifier {
    fun askToResume()
}

/**
 * Starts reporting again for the trip stored on the phone after a reboot or an update of the app, because Android
 * restarts a START_STICKY service after neither. When it should not or cannot, it posts the resume notification
 * instead, and the app opens on Resume as it does after a force-stop.
 *
 * It does not start the service without background location. From Android 14, a location service started in the
 * background without it throws in startForeground, which crashes the app, and a service that stops before calling
 * startForeground crashes it too: the check has to come before the start.
 *
 * With the app in front it leaves the trip to the app, which opens on Tracking or Resume by itself. From Android 15,
 * opening a force-stopped app delivers the BOOT_COMPLETED it missed, and a force-stopped trip asks before resuming.
 */
class RestartTracking(
    private val tripStateStore: TripStateStore,
    private val serviceController: ServiceController,
    private val resumeNotifier: ResumeNotifier,
    private val clock: () -> Long,
    private val canReportInBackground: () -> Boolean,
    private val appInFront: () -> Boolean,
) {
    suspend fun restart(): RestartOutcome {
        val trip = tripStateStore.activeTrip.first() ?: return RestartOutcome.NoTrip
        if (appInFront()) return RestartOutcome.AppInFront
        val ageSec = clock() - trip.startedAtEpochSec
        if (ageSec >= MAX_RESTART_AGE.seconds) {
            Log.i(TAG, "Not restarting a trip started ${ageSec}s ago")
            return askDriver()
        }
        if (!canReportInBackground()) {
            Log.i(TAG, "Not restarting without background location")
            return askDriver()
        }
        // Only the two ways Android documents refusing the start: anything else is a bug, and should not pass for one.
        try {
            serviceController.startTracking()
        } catch (e: IllegalStateException) {
            // ForegroundServiceStartNotAllowedException, from Android 12, is one.
            Log.w(TAG, "Android refused to start the tracking service from the background", e)
            return askDriver()
        } catch (e: SecurityException) {
            Log.w(TAG, "Android refused the tracking service a permission it needs", e)
            return askDriver()
        }
        return RestartOutcome.Started
    }

    private fun askDriver(): RestartOutcome {
        resumeNotifier.askToResume()
        return RestartOutcome.AskedDriver
    }
}
