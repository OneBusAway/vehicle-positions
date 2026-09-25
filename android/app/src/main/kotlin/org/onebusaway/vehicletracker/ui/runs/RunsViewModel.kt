package org.onebusaway.vehicletracker.ui.runs

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.CatalogRepository
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.recordLocally
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.ARG_ROUTE_ID
import org.onebusaway.vehicletracker.ui.ARG_VEHICLE_ID
import java.time.Instant
import javax.inject.Inject

/** Why a run could not be started, in the terms the driver is shown. */
enum class TripError { NOT_ASSIGNED, TRIP_ACTIVE, NETWORK, NO_GEOMETRY, TRIP_NOT_ACTIVE, OTHER }

fun Throwable.toTripError(): TripError = when {
    this is ApiError.NotAssigned -> TripError.NOT_ASSIGNED
    this is ApiError.TripAlreadyActive -> TripError.TRIP_ACTIVE
    this is ApiError.TripNotActiveToday -> TripError.TRIP_NOT_ACTIVE
    this is ApiError.Other && msg == "network" -> TripError.NETWORK
    else -> TripError.OTHER
}

sealed interface RunsUiState {
    data object Loading : RunsUiState

    data class Loaded(
        val page: RunPage,
        /** The run to badge and scroll to, and which badge it carries; both null once the day is over. */
        val highlightedId: String?,
        val highlight: RunHighlight?,
        val starting: Boolean,
        val error: TripError?,
    ) : RunsUiState

    data class Error(val retry: Boolean) : RunsUiState
}

@HiltViewModel
class RunsViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val catalogRepository: CatalogRepository,
    private val tripRepository: TripRepository,
    private val tripGeometryStore: TripGeometryStore,
    private val serviceController: ServiceController,
    @param:EpochSecondsClock private val clock: () -> Long,
) : ViewModel() {
    // Read here rather than handed in by the screen, so a rotation reuses the ViewModel and its
    // already-loaded runs instead of refetching them. The nav graph guarantees both are present.
    private val vehicleId: String = checkNotNull(savedStateHandle[ARG_VEHICLE_ID]) { "no $ARG_VEHICLE_ID argument" }
    private val routeId: String = checkNotNull(savedStateHandle[ARG_ROUTE_ID]) { "no $ARG_ROUTE_ID argument" }

    /** null while a load is in flight; otherwise the outcome of the last one. */
    private val loadResult = MutableStateFlow<Result<RunPage>?>(null)
    private val starting = MutableStateFlow(false)
    private val startError = MutableStateFlow<TripError?>(null)

    val uiState: StateFlow<RunsUiState> = combine(
        loadResult,
        starting,
        startError,
    ) { result, startInFlight, error ->
        when {
            result == null -> RunsUiState.Loading
            result.isSuccess -> {
                val page = result.getOrThrow()
                val now = Instant.ofEpochSecond(clock())
                val highlighted = highlightedRun(page.runs, now)
                RunsUiState.Loaded(
                    page = page,
                    highlightedId = highlighted?.id,
                    highlight = highlighted?.let { highlightOf(it, now) },
                    starting = startInFlight,
                    error = error,
                )
            }
            else -> RunsUiState.Error(retry = result.exceptionOrNull() !is ApiError.Unauthorized)
        }
    }.stateIn(viewModelScope, SharingStarted.Eagerly, RunsUiState.Loading)

    init {
        load()
    }

    fun retry() = load()

    private fun load() {
        loadResult.value = null
        // Whatever refused a start against the old list — a run gone out of service — is not
        // true of the new one.
        startError.value = null
        viewModelScope.launch {
            // mapCatching: a zone or a schedule time this platform cannot read is a bad reply,
            // not a crash — the screen offers a retry as it would for any other failed load.
            loadResult.value = catalogRepository.trips(routeId).mapCatching { it.toRunPage() }
        }
    }

    /**
     * Starts [runId] on the route the catalog listed it under, on the service date the catalog
     * dated the list — the three things the feed's `TripDescriptor` ends up carrying.
     */
    fun onStartRun(runId: String, onStarted: () -> Unit) {
        // A second tap on a list whose rows are still enabled would come back 409.
        if (starting.value) return
        val serviceDate = (uiState.value as? RunsUiState.Loaded)?.page?.serviceDate ?: return
        starting.value = true
        startError.value = null
        viewModelScope.launch {
            when (val error = startRun(runId, serviceDate)) {
                null -> {
                    serviceController.startTracking()
                    starting.value = false
                    onStarted()
                }
                else -> {
                    starting.value = false
                    startError.value = error
                }
            }
        }
    }

    /**
     * Fetches and checks the run's geometry before telling the server anything, as iOS's
     * `TripSession.start` does: if there is nothing to judge adherence against, nothing should be
     * started, so a retry does not run into "trip already active". Null once the run has started.
     */
    private suspend fun startRun(runId: String, serviceDate: String): TripError? {
        val geometry = catalogRepository.trip(runId).getOrElse { return it.toTripError() }
        // The server dates the run by the day it runs on now. If the service day has rolled over
        // at 03:00 since the list was loaded, that is not the list's date, and the feed would
        // carry one day's start_date against the other day's schedule.
        if (geometry.serviceDate != serviceDate) return TripError.TRIP_NOT_ACTIVE
        if (AdherenceEvaluator.of(geometry) == null) return TripError.NO_GEOMETRY
        tripRepository.start(vehicleId, routeId, runId, serviceDate).onFailure { return it.toTripError() }
        // Best-effort, as the other writes after a start are: without it the trip still reports,
        // and the tracking screen says the schedule is unavailable.
        recordLocally("trip geometry") { tripGeometryStore.save(geometry) }
        return null
    }
}
