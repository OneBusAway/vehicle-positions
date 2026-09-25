package org.onebusaway.vehicletracker.ui.tracking

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.TripStateStore
import javax.inject.Inject

data class TrackingUiState(
    val tracking: TrackingState = TrackingState(),
    val activeTrip: ActiveTrip? = null,
    val ending: Boolean = false,
    val endTripError: Boolean = false,
)

@HiltViewModel
class TrackingViewModel @Inject constructor(
    trackingRepository: TrackingRepository,
    tripStateStore: TripStateStore,
    private val tripEnder: TripEnder,
) : ViewModel() {
    private val _uiState = MutableStateFlow(TrackingUiState())
    val uiState: StateFlow<TrackingUiState> = _uiState.asStateFlow()

    init {
        combine(trackingRepository.state, tripStateStore.activeTrip, tripEnder.state) { tracking, trip, end ->
            TrackingUiState(tracking = tracking, activeTrip = trip, ending = end.ending, endTripError = end.failed)
        }
            .onEach { _uiState.value = it }
            .launchIn(viewModelScope)
    }

    fun onEndTrip(onEnded: () -> Unit) {
        val trip = _uiState.value.activeTrip ?: return
        viewModelScope.launch { tripEnder.end(trip, onEnded) }
    }

    fun onEndTripLocally(onEnded: () -> Unit) {
        viewModelScope.launch { tripEnder.endLocally(onEnded) }
    }

    fun dismissEndTripError() = tripEnder.dismissError()
}
