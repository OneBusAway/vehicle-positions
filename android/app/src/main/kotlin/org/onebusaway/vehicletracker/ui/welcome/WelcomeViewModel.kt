package org.onebusaway.vehicletracker.ui.welcome

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverFlagsStore
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.SessionStore
import java.io.IOException
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject

private const val TAG = "WelcomeViewModel"

/** Leaving the welcome, by Skip, by Continue on its last screen or by Back on its first. */
@HiltViewModel
class WelcomeViewModel @Inject constructor(
    private val sessionStore: SessionStore,
    private val driverFlags: DriverFlagsStore,
) : ViewModel() {
    private val leaving = AtomicBoolean(false)
    private val _left = MutableStateFlow(false)

    /**
     * True once the driver has left and the phone has recorded it. Read as state rather than called back, as Sign in's
     * result is: the screen that asked may be gone after a rotation, and the one in its place reads this.
     */
    val left: StateFlow<Boolean> = _left.asStateFlow()

    /**
     * [marksSeen] records that the signed-in driver has seen the welcome, so it is not shown to them again. It is
     * false when the welcome was opened to be read again, which changes nothing.
     */
    fun leave(marksSeen: Boolean) {
        // A second tap already on its way must not record it twice.
        if (!leaving.compareAndSet(false, true)) return
        viewModelScope.launch {
            if (marksSeen) markSeen()
            _left.value = true
        }
    }

    private suspend fun markSeen() {
        val driver = DriverKey.of(sessionStore.session.first()) ?: return
        try {
            driverFlags.set(driver, DriverFlag.WelcomeSeen)
        } catch (e: IOException) {
            // As on a full disk. The driver goes on to their trip, and sees the welcome once more.
            AppLog.w(TAG, "Could not record that the welcome was seen; it will show again", e)
        }
    }
}
