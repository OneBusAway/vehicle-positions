package org.onebusaway.vehicletracker.ui.tracking

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.service.ServiceController
import javax.inject.Inject

data class EndTripState(val ending: Boolean = false, val failed: Boolean = false)

/**
 * Ends the active trip for every screen that offers to: the tracking screen and the prompt after
 * an interrupted shift. One implementation, so an end the server does not confirm is handled the
 * same way from either — retry, or end on this device only.
 *
 * Unscoped, so each ViewModel gets its own and one screen's failed end never shows on another.
 */
class TripEnder @Inject constructor(
    private val tripRepository: TripRepository,
    private val tripStateStore: TripStateStore,
    private val tripGeometryStore: TripGeometryStore,
    private val serviceController: ServiceController,
) {
    private val _state = MutableStateFlow(EndTripState())
    val state: StateFlow<EndTripState> = _state.asStateFlow()

    suspend fun end(trip: ActiveTrip, onEnded: () -> Unit) {
        if (_state.value.ending) return
        _state.value = EndTripState(ending = true)
        tripRepository.end(trip.tripDbId).fold(
            onSuccess = {
                tripGeometryStore.clear()
                serviceController.stopTracking()
                _state.value = EndTripState()
                onEnded()
            },
            onFailure = {
                _state.value = EndTripState(failed = true)
            },
        )
    }

    suspend fun endLocally(onEnded: () -> Unit) {
        tripStateStore.clearActiveTrip()
        tripGeometryStore.clear()
        serviceController.stopTracking()
        _state.value = EndTripState()
        onEnded()
    }

    fun dismissError() = _state.update { it.copy(failed = false) }
}
