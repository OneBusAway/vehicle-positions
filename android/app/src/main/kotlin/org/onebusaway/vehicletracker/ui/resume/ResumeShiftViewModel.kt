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
import kotlinx.coroutines.flow.first
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.loadFor
import org.onebusaway.vehicletracker.data.sameServer
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

/** Where the prompt goes once the driver's choice has played out. */
enum class ResumeOutcome {
    /** Reporting again: on to Tracking. */
    RESUMED,

    /** Signed in to another server than the trip's: Sign in for the trip's server first, then back here. */
    SIGN_IN_FIRST,

    /** The trip has ended: on as the app would open with no trip. */
    ENDED,
}

data class ResumeShiftUiState(
    val trip: ActiveTrip? = null,
    /** Null until the stored trip and its schedule are read off the phone. */
    val card: ResumeCard? = null,
    val ending: Boolean = false,
    val endTripError: Boolean = false,
    /** Where the screen goes on to, until it says it has with [ResumeShiftViewModel.onNavigated]. */
    val outcome: ResumeOutcome? = null,
)

/**
 * The prompt for a stored trip whose tracking service is not running: resume it, or end it. Each choice ends in
 * [ResumeShiftUiState.outcome] rather than calling the screen back: an end can be confirmed after a rotation, and the
 * screen that asked is gone by then. The one showing then is the one to go on.
 */
@HiltViewModel
class ResumeShiftViewModel @Inject constructor(
    tripStateStore: TripStateStore,
    tripGeometryStore: TripGeometryStore,
    private val sessionStore: SessionStore,
    private val serviceController: ServiceController,
    private val tripEnder: TripEnder,
    @param:EpochSecondsClock private val clock: () -> Long,
    phoneZone: ZoneId,
) : ViewModel() {
    private val _uiState = MutableStateFlow(ResumeShiftUiState())
    val uiState: StateFlow<ResumeShiftUiState> = _uiState.asStateFlow()

    private val resumeOutcome = MutableStateFlow<ResumeOutcome?>(null)

    /** From a tap on Resume until the screen has gone on to Sign in, or for good once reporting has started again. */
    private var resuming = false

    init {
        val trips = tripStateStore.activeTrip.map { trip -> trip to trip?.let { ResumeCard.of(it, tripGeometryStore.loadFor(it), phoneZone) } }
        combine(trips, tripEnder.state, resumeOutcome) { (trip, card), end, resumed ->
            ResumeShiftUiState(
                trip = trip,
                card = card,
                ending = end.ending,
                endTripError = end.failed,
                outcome = if (end.ended) ResumeOutcome.ENDED else resumed,
            )
        }
            .onEach { _uiState.value = it }
            .launchIn(viewModelScope)
    }

    /** Now, for the "Started 23 min ago" that the screen ticks once a minute. */
    fun nowEpochSec(): Long = clock()

    /**
     * Restarts reporting without telling the server, as iOS does: the trip was never ended there,
     * and a second `/trips/start` would be refused as a trip already active.
     *
     * Reports go to the server the phone is signed in to. When that is not the trip's, as on a phone restored from
     * backup, which keeps the trip but not the session, the prompt opens Sign in for the trip's server instead.
     */
    fun onResumeShift() {
        val trip = _uiState.value.trip ?: return
        // A second tap, or one on the screen on its way out to Tracking, must not start the reports again.
        if (resuming) return
        resuming = true
        viewModelScope.launch {
            val signedInTo = sessionStore.session.first().serverUrl
            if (trip.serverUrl != null && !sameServer(trip.serverUrl, signedInTo)) {
                resumeOutcome.value = ResumeOutcome.SIGN_IN_FIRST
            } else {
                serviceController.startTracking()
                resumeOutcome.value = ResumeOutcome.RESUMED
            }
        }
    }

    fun onEndShift() {
        val trip = _uiState.value.trip ?: return
        viewModelScope.launch { tripEnder.end(trip) }
    }

    fun onEndShiftLocally() {
        viewModelScope.launch { tripEnder.endLocally() }
    }

    /** Back from a failed end to Resume and End trip, with the trip still open. */
    fun dismissEndShiftError() = tripEnder.dismissError()

    /** The screen has gone on, so one recreated later, or shown again after Sign in, does not go on again. */
    fun onNavigated() {
        // Back from Sign in, the driver taps Resume again.
        if (resumeOutcome.value == ResumeOutcome.SIGN_IN_FIRST) resuming = false
        resumeOutcome.value = null
        tripEnder.onNavigated()
    }
}
