package org.onebusaway.vehicletracker.ui.tracking

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.BatteryManager
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LifecycleStartEffect

/**
 * Whether the display stays on: while Tracking is on screen and the phone is plugged in, as on iOS. [plugged] is
 * [BatteryManager.EXTRA_PLUGGED], 0 on battery. Plugged in, not charging: a phone on its charger that has stopped
 * charging, full or held at a limit, stays on too, as with Android's own Stay awake while charging.
 */
internal fun keepsScreenOn(trackingShowing: Boolean, plugged: Int): Boolean = trackingShowing && plugged != 0

/**
 * Keeps the display on while this is on screen and the phone is plugged in: a phone in a dash mount goes dark after
 * the screen timeout, and the driver should not have to reach over to wake it. [android.view.View.keepScreenOn] holds
 * only while the window is visible, and is cleared when Tracking goes away. The battery broadcast is listened to only
 * while the screen is started, which also lets go when the app goes to the background.
 */
@Composable
internal fun KeepScreenOnWhileCharging() {
    val context = LocalContext.current
    val view = LocalView.current
    var showing by remember { mutableStateOf(false) }
    var plugged by remember { mutableIntStateOf(0) }
    LifecycleStartEffect(context) {
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                plugged = intent.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0)
            }
        }
        // The broadcast is sticky: registering for it also delivers the current state.
        ContextCompat.registerReceiver(
            context,
            receiver,
            IntentFilter(Intent.ACTION_BATTERY_CHANGED),
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )
        showing = true
        onStopOrDispose {
            showing = false
            context.unregisterReceiver(receiver)
        }
    }
    val keepOn = keepsScreenOn(showing, plugged)
    DisposableEffect(view, keepOn) {
        view.keepScreenOn = keepOn
        onDispose { view.keepScreenOn = false }
    }
}
