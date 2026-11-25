package org.onebusaway.vehicletracker.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.ScreenOn
import org.onebusaway.vehicletracker.data.TripSettings
import org.onebusaway.vehicletracker.data.TripSettingsStore
import java.io.IOException
import javax.inject.Inject

private const val TAG = "TripSettingsViewModel"

/** What the driver chose for Tracking: shown in Settings, changed in During a trip, and followed by Tracking. */
@HiltViewModel
class TripSettingsViewModel @Inject constructor(private val store: TripSettingsStore) : ViewModel() {
    /** Null until read. */
    val settings: StateFlow<TripSettings?> = store.settings.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    fun onScreenOn(screenOn: ScreenOn) {
        save { store.setScreenOn(screenOn) }
    }

    fun onMapUp(mapUp: MapUp) {
        save { store.setMapUp(mapUp) }
    }

    fun onVibrates(vibrates: Boolean) {
        save { store.setVibrates(vibrates) }
    }

    private fun save(write: suspend () -> Unit) {
        viewModelScope.launch {
            try {
                write()
            } catch (e: IOException) {
                // A full disk, say. The page shows what is stored, so the driver sees that the tap did not take.
                AppLog.w(TAG, "Could not store a trip setting", e)
            }
        }
    }
}
