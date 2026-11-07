package org.onebusaway.vehicletracker.service

import android.app.ActivityManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import javax.inject.Inject
import kotlin.time.Duration.Companion.seconds

private const val TAG = "RestartTracking"

/** Well inside the time Android gives a receiver that has called goAsync. */
private val RESTART_TIMEOUT = 8.seconds

/**
 * Hands a reboot or an update of the app to [RestartTracking], which decides what happens to a trip stored on the
 * phone. Both broadcasts let the app start a foreground service from the background.
 *
 * BOOT_COMPLETED rather than LOCKED_BOOT_COMPLETED: the trip is kept in credential-encrypted storage, which can be read
 * only once the phone has been unlocked after it started.
 */
@AndroidEntryPoint
class RestartTrackingReceiver : BroadcastReceiver() {
    @Inject lateinit var restartTracking: RestartTracking
    @Inject lateinit var scope: CoroutineScope

    // Hilt's Gradle plugin injects the fields above by adding a call to super.onReceive here, which Kotlin cannot
    // write itself because BroadcastReceiver declares onReceive abstract.
    override fun onReceive(context: Context, intent: Intent) {
        val pending = goAsync()
        scope.launch {
            try {
                val outcome = withTimeoutOrNull(RESTART_TIMEOUT) { restartTracking.restart() }
                if (outcome == null) {
                    Log.w(TAG, "${intent.action}: gave up after $RESTART_TIMEOUT")
                } else {
                    Log.i(TAG, "${intent.action}: $outcome")
                }
            } finally {
                pending.finish()
            }
        }
    }
}

/**
 * Whether one of the app's screens is in front, as Android ranks the process. A process started only to receive a
 * broadcast ranks lower, so this is false on a real boot or update even though the receiver is running.
 */
internal fun appInFront(): Boolean {
    val process = ActivityManager.RunningAppProcessInfo()
    ActivityManager.getMyMemoryState(process)
    return process.importance == ActivityManager.RunningAppProcessInfo.IMPORTANCE_FOREGROUND
}
