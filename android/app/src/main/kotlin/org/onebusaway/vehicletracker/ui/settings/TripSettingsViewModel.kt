package org.onebusaway.vehicletracker.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.ScreenOn
import org.onebusaway.vehicletracker.data.TripSettings
import org.onebusaway.vehicletracker.data.TripSettingsStore
import javax.inject.Inject

/** What the driver chose for Tracking: shown in Settings, changed in During a trip, and followed by Tracking. */
@HiltViewModel
class TripSettingsViewModel @Inject constructor(private val store: TripSettingsStore) : ViewModel() {
    /** The defaults until read. */
    val settings: StateFlow<TripSettings> = store.settings.stateIn(viewModelScope, SharingStarted.Eagerly, TripSettings())

    fun onScreenOn(screenOn: ScreenOn) {
        viewModelScope.launch { store.setScreenOn(screenOn) }
    }

    fun onMapUp(mapUp: MapUp) {
        viewModelScope.launch { store.setMapUp(mapUp) }
    }

    fun onVibrates(vibrates: Boolean) {
        viewModelScope.launch { store.setVibrates(vibrates) }
    }
}
