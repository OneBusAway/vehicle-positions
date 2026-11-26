package org.onebusaway.vehicletracker.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.map.OfflineMapSource
import javax.inject.Inject

/** What Settings knows of the map: still being read, or [map], null when there is none to show. */
internal sealed interface OfflineMapView {
    data object Reading : OfflineMapView

    data class Read(val map: OfflineMap?) : OfflineMapView
}

/** The agency's street map, for its row in Settings and its own page. */
@HiltViewModel
class OfflineMapViewModel @Inject constructor(private val maps: OfflineMapSource) : ViewModel() {
    internal val map: StateFlow<OfflineMapView> = maps.state
        .map { state ->
            val here = state is MapFileState.Ready && state.update == null
            OfflineMapView.Read(offlineMap(state, if (here) maps.details() else null))
        }
        .stateIn(viewModelScope, SharingStarted.Eagerly, OfflineMapView.Reading)

    fun onGetAgain() = maps.getAgain()

    fun onDelete() = maps.delete()

    fun onTryAgain() = maps.refresh()
}
