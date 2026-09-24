package org.onebusaway.vehicletracker.ui.runs

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
    private val catalogRepository: CatalogRepository,
    private val tripRepository: TripRepository,
    private val serviceController: ServiceController,
    @param:EpochSecondsClock private val clock: () -> Long,
) : ViewModel() {
    /** null while a load is in flight; otherwise the outcome of the last one. */
    private val loadResult = MutableStateFlow<Result<RunPage>?>(null)
    private val starting = MutableStateFlow(false)
    private val startError = MutableStateFlow<TripError?>(null)
    private var routeId: String? = null

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

    fun load(routeId: String) {
        this.routeId = routeId
        loadResult.value = null
        viewModelScope.launch {
            // mapCatching: a zone or a schedule time this platform cannot read is a bad reply,
            // not a crash — the screen offers a retry as it would for any other failed load.
            loadResult.value = catalogRepository.trips(routeId).mapCatching { it.toRunPage() }
        }
    }

    fun retry() {
        routeId?.let { load(it) }
    }

    /**
     * Starts [runId] on [vehicleId] with the route the catalog listed it under — the ids the
     * feed's `TripDescriptor` ends up carrying.
     */
    fun onStartRun(vehicleId: String, runId: String, onStarted: () -> Unit) {
        // A second tap on a list whose rows are still enabled would come back 409.
        if (starting.value) return
        val route = routeId ?: return
        starting.value = true
        startError.value = null
        // TODO(phase 2): fetch GET /api/v1/gtfs/trips/{id} here and persist the geometry for adherence.
        viewModelScope.launch {
            tripRepository.start(vehicleId, route, runId).fold(
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
