package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.lifecycle.compose.LifecycleResumeEffect
import org.onebusaway.vehicletracker.ui.permissions.grantsOf
import org.onebusaway.vehicletracker.ui.permissions.locationServicesOn

/** What the phone lets the app do, as last read. */
@Stable
internal class PhoneAccessState(private val read: () -> PhoneAccess) {
    var value by mutableStateOf(read())
        private set

    fun refresh() {
        value = read()
    }
}

/**
 * Read again whenever the driver may have changed it: back from the phone's settings, or from its quick settings, which
 * only take the focus.
 */
@Composable
internal fun rememberPhoneAccess(): PhoneAccessState {
    val context = LocalContext.current
    val access = remember(context) { PhoneAccessState { PhoneAccess(grantsOf(context), locationServicesOn(context)) } }
    val focused = LocalWindowInfo.current.isWindowFocused
    LifecycleResumeEffect(access, focused) {
        access.refresh()
        onPauseOrDispose {}
    }
    return access
}
