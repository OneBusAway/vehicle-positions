package org.onebusaway.vehicletracker.ui.resume

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
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import javax.inject.Inject

data class ResumeShiftUiState(
    val trip: ActiveTrip? = null,
    /** Read when the prompt opens and not ticked: the prompt is there to be answered, not watched. */
    val startedAgoSec: Long = 0,
    val ending: Boolean = false,
    val endTripError: Boolean = false,
)

/** The prompt for a stored trip whose tracking service is not running: resume it, or end it. */
@HiltViewModel
class ResumeShiftViewModel @Inject constructor(
    tripStateStore: TripStateStore,
    private val serviceController: ServiceController,
    private val tripEnder: TripEnder,
    @param:EpochSecondsClock private val clock: () -> Long,
) : ViewModel() {
    private val _uiState = MutableStateFlow(ResumeShiftUiState())
    val uiState: StateFlow<ResumeShiftUiState> = _uiState.asStateFlow()

    init {
        combine(tripStateStore.activeTrip, tripEnder.state) { trip, end ->
            ResumeShiftUiState(
                trip = trip,
                startedAgoSec = trip?.let { (clock() - it.startedAtEpochSec).coerceAtLeast(0) } ?: 0,
                ending = end.ending,
                endTripError = end.failed,
            )
        }
            .onEach { _uiState.value = it }
            .launchIn(viewModelScope)
    }

    /**
     * Restarts reporting without telling the server, as iOS does: the trip was never ended there,
     * and a second `/trips/start` would be refused as a trip already active.
     */
    fun onResumeShift(onResumed: () -> Unit) {
        serviceController.startTracking()
        onResumed()
    }

    fun onEndShift(onEnded: () -> Unit) {
        val trip = _uiState.value.trip ?: return
        viewModelScope.launch { tripEnder.end(trip, onEnded) }
    }

    fun onEndShiftLocally(onEnded: () -> Unit) {
        viewModelScope.launch { tripEnder.endLocally(onEnded) }
    }

    fun dismissEndShiftError() = tripEnder.dismissError()
}
