package org.onebusaway.vehicletracker.ui.tracking

import android.content.Context
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.flow.Flow
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TripSettings

private const val VIBRATION_MS = 200L

/** A problem that appears, or gives way to another, vibrates the phone; one that clears does not, as on iOS. */
internal fun vibratesFor(before: TrackingProblem, problem: TrackingProblem): Boolean =
    problem != TrackingProblem.NONE && problem != before

/** Whether [chosen] has the phone vibrate. Not until it is read, as the driver may have switched it off. */
internal fun vibrationOn(chosen: TripSettings?): Boolean = chosen?.vibrates ?: false

/**
 * Vibrates once for each problem in [problems] that comes up while Tracking is on screen, while it is [on]. Not for one
 * that is there already as the screen opens or comes back from the background, nor again after a rotation: each time
 * the screen starts, it starts from the problem there is then.
 */
@Composable
internal fun VibrateOnProblem(problems: Flow<TrackingProblem>, on: Boolean, vibrate: () -> Unit) {
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val vibrates by rememberUpdatedState(on)
    val latestVibrate by rememberUpdatedState(vibrate)
    LaunchedEffect(problems, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            var last: TrackingProblem? = null
            problems.collect { problem ->
                val before = last
                last = problem
                if (before != null && vibrates && vibratesFor(before, problem)) latestVibrate()
            }
        }
    }
}

/** One short vibration of the phone, which does nothing on one without a vibrator. */
internal fun problemVibration(context: Context): () -> Unit {
    val vibrator = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        context.getSystemService(VibratorManager::class.java)?.defaultVibrator
    } else {
        context.getSystemService(Vibrator::class.java)
    }
    return { vibrator?.vibrate(VibrationEffect.createOneShot(VIBRATION_MS, VibrationEffect.DEFAULT_AMPLITUDE)) }
}
