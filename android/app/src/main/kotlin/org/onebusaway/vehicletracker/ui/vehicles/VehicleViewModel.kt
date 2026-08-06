package org.onebusaway.vehicletracker.ui.vehicles

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.VehicleRepository
import org.onebusaway.vehicletracker.data.api.VehicleDto
import javax.inject.Inject

sealed interface VehiclesUiState {
    data object Loading : VehiclesUiState
    data class Loaded(val vehicles: List<VehicleDto>) : VehiclesUiState
    data class Error(val retry: Boolean) : VehiclesUiState
}

@HiltViewModel
class VehicleViewModel @Inject constructor(
    private val vehicleRepository: VehicleRepository,
) : ViewModel() {
    private val _uiState = MutableStateFlow<VehiclesUiState>(VehiclesUiState.Loading)
    val uiState: StateFlow<VehiclesUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun retry() = load()

    private fun load() {
        _uiState.value = VehiclesUiState.Loading
        viewModelScope.launch {
            val result = vehicleRepository.myVehicles()
            result.fold(
                onSuccess = { vehicles -> _uiState.value = VehiclesUiState.Loaded(vehicles) },
                onFailure = { error ->
                    val retryable = error !is ApiError.Unauthorized
                    _uiState.value = VehiclesUiState.Error(retry = retryable)
                },
            )
        }
    }
}
