package org.onebusaway.vehicletracker.ui.runs

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.CatalogRepository
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.recordLocally
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.ARG_ROUTE_COLOR
import org.onebusaway.vehicletracker.ui.ARG_ROUTE_ID
import org.onebusaway.vehicletracker.ui.ARG_ROUTE_NAME
import org.onebusaway.vehicletracker.ui.ARG_ROUTE_TEXT_COLOR
import org.onebusaway.vehicletracker.ui.ARG_VEHICLE_ID
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import java.time.Instant
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import javax.inject.Inject

private const val SECONDS_PER_MINUTE = 60
private const val MILLIS_PER_SECOND = 1000L

/** Why a run could not be started, in the terms the driver is shown. */
enum class TripError { NOT_ASSIGNED, TRIP_ACTIVE, PREVIOUS_TRIP_NOT_ENDED, NETWORK, NO_GEOMETRY, TRIP_NOT_ACTIVE, OTHER }

fun Throwable.toTripError(): TripError = when {
    this is ApiError.NotAssigned -> TripError.NOT_ASSIGNED
    this is ApiError.TripAlreadyActive -> TripError.TRIP_ACTIVE
    this is ApiError.PreviousTripNotEnded -> TripError.PREVIOUS_TRIP_NOT_ENDED
    this is ApiError.TripNotActiveToday -> TripError.TRIP_NOT_ACTIVE
    this is ApiError.Other && msg == "network" -> TripError.NETWORK
    else -> TripError.OTHER
}

/** How a start came out. A session the server has ended has no banner: the screen leaves for Sign in. */
private sealed interface StartOutcome {
    data object Started : StartOutcome
    data class Refused(val error: TripError) : StartOutcome
    data object SessionEnded : StartOutcome
}

private fun Throwable.toStartOutcome(): StartOutcome =
    if (this is ApiError.Unauthorized) StartOutcome.SessionEnded else StartOutcome.Refused(toTripError())

sealed interface RunsUiState {
    data object Loading : RunsUiState

    data class Loaded(
        val page: RunPage,
        /** [page]'s runs as departure boards, one per direction when they split into two. */
        val boards: List<Board>,
        /** The index of the board the screen opens on. */
        val startBoard: Int,
        /** The run a start is in flight for, or null. */
        val startingRunId: String?,
        val error: TripError?,
    ) : RunsUiState {
        val starting: Boolean get() = startingRunId != null
    }

    data object Error : RunsUiState

    /** The server has ended the session and the tokens are gone, so the screen opens Sign in. */
    data object SessionEnded : RunsUiState
}

@HiltViewModel
class RunsViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val catalogRepository: CatalogRepository,
    private val tripRepository: TripRepository,
    private val tripGeometryStore: TripGeometryStore,
    private val serviceController: ServiceController,
    private val authRepository: AuthRepository,
    @param:EpochSecondsClock private val clock: () -> Long,
) : ViewModel() {
    // Read here rather than handed in by the screen, so a rotation reuses the ViewModel and its
    // already-loaded runs instead of refetching them. The nav graph guarantees both are present.
    /** The bus the run would start on, which a refusal names. */
    val vehicleId: String = checkNotNull(savedStateHandle[ARG_VEHICLE_ID]) { "no $ARG_VEHICLE_ID argument" }
    private val routeId: String = checkNotNull(savedStateHandle[ARG_ROUTE_ID]) { "no $ARG_ROUTE_ID argument" }

    /** The route's badge, handed over by the route step: the runs reply names only the route's id. */
    val route: RouteBadgeModel = RouteBadgeModel.of(
        shortName = savedStateHandle[ARG_ROUTE_NAME] ?: "",
        color = savedStateHandle[ARG_ROUTE_COLOR] ?: "",
        textColor = savedStateHandle[ARG_ROUTE_TEXT_COLOR] ?: "",
    )

    // The driver's own way of writing a time, read once: the board formats every run's start when it is built.
    private val timeFormat = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT)

    /** null while a load is in flight; otherwise the outcome of the last one. */
    private val loadResult = MutableStateFlow<Result<RunPage>?>(null)

    /** The time the boards are built for: set by each load, and moved on each minute while the step is on screen. */
    private val now = MutableStateFlow(Instant.ofEpochSecond(clock()))
    private var ticking: Job? = null

    private val startingRun = MutableStateFlow<String?>(null)
    private val startError = MutableStateFlow<TripError?>(null)
    private val sessionEnded = MutableStateFlow(false)

    private val boards = combine(loadResult, now) { result, now ->
        result?.mapCatching { page -> page to departureBoardsOf(page, now, timeFormat) }
    }

    val uiState: StateFlow<RunsUiState> = combine(
        sessionEnded,
        boards,
        startingRun,
        startError,
    ) { ended, result, startingRunId, error ->
        when {
            ended -> RunsUiState.SessionEnded
            result == null -> RunsUiState.Loading
            result.isSuccess -> {
                val (page, boards) = result.getOrThrow()
                RunsUiState.Loaded(
                    page = page,
                    boards = boards.boards,
                    startBoard = boards.start,
                    startingRunId = startingRunId,
                    error = error,
                )
            }
            else -> RunsUiState.Error
        }
    }.stateIn(viewModelScope, SharingStarted.Eagerly, RunsUiState.Loading)

    init {
        load()
    }

    fun retry() = load()

    /**
     * Runs go by while the driver looks at the board, so while this step is on screen its boards are built again at
     * each new minute: which runs are dimmed as gone, where the Now line sits, and which run is tagged Now or Next.
     */
    fun onScreenVisible(visible: Boolean) {
        ticking?.cancel()
        ticking = if (visible) viewModelScope.launch { tickEachMinute() } else null
    }

    private suspend fun tickEachMinute() {
        while (true) {
            now.value = Instant.ofEpochSecond(clock())
            delay((SECONDS_PER_MINUTE - clock() % SECONDS_PER_MINUTE) * MILLIS_PER_SECOND)
        }
    }

    private fun load() {
        loadResult.value = null
        // Whatever refused a start against the old list — a run gone out of service — is not
        // true of the new one.
        startError.value = null
        viewModelScope.launch {
            // mapCatching: a zone or a schedule time this platform cannot read is a bad reply,
            // not a crash — the screen offers a retry as it would for any other failed load.
            val result = catalogRepository.trips(routeId).mapCatching { it.toRunPage() }
            if (result.exceptionOrNull() is ApiError.Unauthorized) endSession()
            now.value = Instant.ofEpochSecond(clock())
            loadResult.value = result
        }
    }

    /**
     * The client has already tried a refresh. The tokens go before the state changes, so nothing can read a dead one
     * once the session shows as ended.
     */
    private suspend fun endSession() {
        authRepository.endSessionLocally()
        sessionEnded.value = true
    }

    /**
     * Starts [runId] on the route the catalog listed it under, on the service date the catalog
     * dated the list — the three things the feed's `TripDescriptor` ends up carrying.
     */
    fun onStartRun(runId: String, onStarted: () -> Unit) {
        // A second tap on a list whose rows are still enabled would come back 409.
        if (startingRun.value != null) return
        val serviceDate = (uiState.value as? RunsUiState.Loaded)?.page?.serviceDate ?: return
        startingRun.value = runId
        startError.value = null
        viewModelScope.launch {
            when (val outcome = startRun(runId, serviceDate)) {
                StartOutcome.Started -> {
                    serviceController.startTracking()
                    startingRun.value = null
                    onStarted()
                }
                is StartOutcome.Refused -> {
                    startingRun.value = null
                    startError.value = outcome.error
                }
                StartOutcome.SessionEnded -> {
                    endSession()
                    startingRun.value = null
                }
            }
        }
    }

    /**
     * Fetches and checks the run's geometry before telling the server anything, as iOS's
     * `TripSession.start` does: if there is nothing to judge adherence against, nothing should be
     * started, so a retry does not run into "trip already active".
     */
    private suspend fun startRun(runId: String, serviceDate: String): StartOutcome {
        val geometry = catalogRepository.trip(runId).getOrElse { return it.toStartOutcome() }
        // The server dates the run by the day it runs on now. If the service day has rolled over
        // at 03:00 since the list was loaded, that is not the list's date, and the feed would
        // carry one day's start_date against the other day's schedule.
        if (geometry.serviceDate != serviceDate) return StartOutcome.Refused(TripError.TRIP_NOT_ACTIVE)
        if (AdherenceEvaluator.of(geometry) == null) return StartOutcome.Refused(TripError.NO_GEOMETRY)
        tripRepository.start(vehicleId, routeId, runId, serviceDate).onFailure { return it.toStartOutcome() }
        // Best-effort, as the other writes after a start are: without it the trip still reports,
        // and the tracking screen says the schedule is unavailable.
        recordLocally("trip geometry") { tripGeometryStore.save(geometry) }
        return StartOutcome.Started
    }
}
