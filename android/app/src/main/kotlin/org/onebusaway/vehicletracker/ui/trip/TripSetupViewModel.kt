package org.onebusaway.vehicletracker.ui.trip

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.service.ServiceController
import javax.inject.Inject

data class TripSetupUiState(
    val routeId: String = "",
    val gtfsTripId: String = "",
    val recentRoutes: List<String> = emptyList(),
    val loading: Boolean = false,
    val error: TripError? = null,
)

enum class TripError { NOT_ASSIGNED, TRIP_ACTIVE, NETWORK, OTHER }

@HiltViewModel
class TripSetupViewModel @Inject constructor(
    private val tripRepository: TripRepository,
    private val tripStateStore: TripStateStore,
    private val serviceController: ServiceController,
) : ViewModel() {
    private val _uiState = MutableStateFlow(TripSetupUiState())
    val uiState: StateFlow<TripSetupUiState> = _uiState.asStateFlow()

    init {
        tripStateStore.recentRoutes
            .onEach { routes -> _uiState.update { it.copy(recentRoutes = routes) } }
            .launchIn(viewModelScope)
    }

    fun onRouteIdChange(value: String) = _uiState.update { it.copy(routeId = value, error = null) }
    fun onGtfsTripIdChange(value: String) = _uiState.update { it.copy(gtfsTripId = value, error = null) }

    fun onStartTrip(vehicleId: String, onStarted: () -> Unit) {
        val state = _uiState.value
        if (state.routeId.isBlank()) {
            _uiState.update { it.copy(error = TripError.OTHER) }
            return
        }
        _uiState.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            val result = tripRepository.start(vehicleId, state.routeId, state.gtfsTripId)
            result.fold(
                onSuccess = {
                    serviceController.startTracking()
                    _uiState.update { it.copy(loading = false, error = null) }
                    onStarted()
                },
                onFailure = { error ->
                    val mapped = when (error) {
                        is ApiError.NotAssigned -> TripError.NOT_ASSIGNED
                        is ApiError.TripAlreadyActive -> TripError.TRIP_ACTIVE
                        is ApiError.Other -> if (error.msg == "network") TripError.NETWORK else TripError.OTHER
                        else -> TripError.OTHER
                    }
                    _uiState.update { it.copy(loading = false, error = mapped) }
                },
            )
        }
    }
}
