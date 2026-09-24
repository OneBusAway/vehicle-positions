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
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.ARG_ROUTE_ID
import org.onebusaway.vehicletracker.ui.ARG_VEHICLE_ID
import java.time.Instant
import javax.inject.Inject

/** Why `POST /api/v1/trips/start` refused, in the terms the driver is shown. */
enum class TripError { NOT_ASSIGNED, TRIP_ACTIVE, NETWORK, OTHER }

fun Throwable.toTripError(): TripError = when {
    this is ApiError.NotAssigned -> TripError.NOT_ASSIGNED
    this is ApiError.TripAlreadyActive -> TripError.TRIP_ACTIVE
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
        // TODO(phase 2): fetch GET /api/v1/gtfs/trips/{id} here and persist the geometry for adherence.
        viewModelScope.launch {
            tripRepository.start(vehicleId, routeId, runId, serviceDate).fold(
                onSuccess = {
                    serviceController.startTracking()
                    starting.value = false
                    onStarted()
                },
                onFailure = { error ->
                    starting.value = false
                    startError.value = error.toTripError()
                },
            )
        }
    }
}
