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
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.service.ServiceController
import javax.inject.Inject

data class TrackingUiState(
    val tracking: TrackingState = TrackingState(),
    val activeTrip: ActiveTrip? = null,
    val ending: Boolean = false,
    val endTripError: Boolean = false,
)

@HiltViewModel
class TrackingViewModel @Inject constructor(
    private val trackingRepository: TrackingRepository,
    private val tripStateStore: TripStateStore,
    private val tripRepository: TripRepository,
    private val serviceController: ServiceController,
) : ViewModel() {
    private val _uiState = MutableStateFlow(TrackingUiState())
    val uiState: StateFlow<TrackingUiState> = _uiState.asStateFlow()

    init {
        combine(trackingRepository.state, tripStateStore.activeTrip) { tracking, trip -> tracking to trip }
            .onEach { (tracking, trip) -> _uiState.update { it.copy(tracking = tracking, activeTrip = trip) } }
            .launchIn(viewModelScope)
    }

    fun onEndTrip(onEnded: () -> Unit) {
        val trip = _uiState.value.activeTrip ?: return
        _uiState.update { it.copy(ending = true, endTripError = false) }
        viewModelScope.launch {
            val result = tripRepository.end(trip.tripDbId)
            result.fold(
                onSuccess = {
                    serviceController.stopTracking()
                    _uiState.update { it.copy(ending = false, endTripError = false) }
                    onEnded()
                },
                onFailure = {
                    _uiState.update { it.copy(ending = false, endTripError = true) }
                },
            )
        }
    }

    fun onEndTripLocally(onEnded: () -> Unit) {
        viewModelScope.launch {
            tripStateStore.clearActiveTrip()
            serviceController.stopTracking()
            _uiState.update { it.copy(ending = false, endTripError = false) }
            onEnded()
        }
    }

    fun dismissEndTripError() = _uiState.update { it.copy(endTripError = false) }
}
