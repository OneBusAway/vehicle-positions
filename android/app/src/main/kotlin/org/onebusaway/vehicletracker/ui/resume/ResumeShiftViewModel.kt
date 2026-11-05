package org.onebusaway.vehicletracker.ui.resume

import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.loadFor
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import java.time.Instant
import java.time.ZoneId
import javax.inject.Inject

/**
 * The open trip as the driver knows it: the route's badge, where it is headed and the run's start time, from the
 * schedule the phone kept. [headsign] and [runStartsAt] are null when the phone has none for the trip, as for one
 * started by an older build; the badge then shows the route's id. Times are read in [zone], the agency's, or the
 * phone's without a schedule.
 */
@Immutable
data class ResumeCard(
    val route: RouteBadgeModel,
    val headsign: String?,
    val runStartsAt: Instant?,
    val vehicleId: String,
    val startedAt: Instant,
    val zone: ZoneId,
) {
    companion object {
        fun of(trip: ActiveTrip, geometry: TripGeometry?, phoneZone: ZoneId) = ResumeCard(
            route = RouteBadgeModel.forTrip(trip, geometry),
            headsign = geometry?.headsign?.ifEmpty { null },
            runStartsAt = geometry?.stops?.firstOrNull()?.departureAt,
            vehicleId = trip.vehicleId,
            startedAt = Instant.ofEpochSecond(trip.startedAtEpochSec),
            zone = geometry?.timezone ?: phoneZone,
        )
    }
}

data class ResumeShiftUiState(
    val trip: ActiveTrip? = null,
    /** Null until the stored trip and its schedule are read off the phone. */
    val card: ResumeCard? = null,
    val ending: Boolean = false,
    val endTripError: Boolean = false,
)

/** The prompt for a stored trip whose tracking service is not running: resume it, or end it. */
@HiltViewModel
class ResumeShiftViewModel @Inject constructor(
    tripStateStore: TripStateStore,
    tripGeometryStore: TripGeometryStore,
    private val serviceController: ServiceController,
    private val tripEnder: TripEnder,
    @param:EpochSecondsClock private val clock: () -> Long,
    phoneZone: ZoneId,
) : ViewModel() {
    private val _uiState = MutableStateFlow(ResumeShiftUiState())
    val uiState: StateFlow<ResumeShiftUiState> = _uiState.asStateFlow()

    init {
        val trips = tripStateStore.activeTrip.map { trip -> trip to trip?.let { ResumeCard.of(it, tripGeometryStore.loadFor(it), phoneZone) } }
        combine(trips, tripEnder.state) { (trip, card), end ->
            ResumeShiftUiState(trip = trip, card = card, ending = end.ending, endTripError = end.failed)
        }
            .onEach { _uiState.value = it }
            .launchIn(viewModelScope)
    }

    /** Now, for the "Started 23 min ago" that the screen ticks once a minute. */
    fun nowEpochSec(): Long = clock()

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

    /** Back from a failed end to Resume and End trip, with the trip still open. */
    fun dismissEndShiftError() = tripEnder.dismissError()
}
