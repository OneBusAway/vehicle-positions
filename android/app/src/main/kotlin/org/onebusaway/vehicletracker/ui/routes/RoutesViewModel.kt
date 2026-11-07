package org.onebusaway.vehicletracker.ui.routes

import androidx.compose.runtime.Immutable
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
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.CatalogRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.api.RouteDto
import javax.inject.Inject

/** One route as the picker draws it: its badge, with the feed's colours already read, and its long name. */
@Immutable
data class RouteItem(val id: String, val badge: RouteBadgeModel, val longName: String)

sealed interface RoutesUiState {
    data object Loading : RoutesUiState

    data class Loaded(
        /** Every route in the catalog, unaffected by [query]. */
        val routes: List<RouteItem>,
        /** [routes] narrowed by [query]. */
        val visible: List<RouteItem>,
        /** Recently driven routes, newest first; empty while the driver is searching. */
        val recent: List<RouteItem>,
        val query: String,
        /** Whether [routes] are drawn as a grid of badges rather than rows; see [showsAsGrid]. */
        val grid: Boolean,
    ) : RoutesUiState

    /**
     * The server has no schedule to pick from. Retrying will not produce one, and there is no
     * manual entry to fall back to: adherence needs a real GTFS trip id, so a trip cannot be
     * started here until an operator configures `GTFS_STATIC_URL`.
     */
    data object NoSchedule : RoutesUiState

    data object Error : RoutesUiState

    /** The server has ended the session and the tokens are gone, so the screen opens Sign in. */
    data object SessionEnded : RoutesUiState
}

@HiltViewModel
class RoutesViewModel @Inject constructor(
    private val catalogRepository: CatalogRepository,
    tripStateStore: TripStateStore,
    private val authRepository: AuthRepository,
) : ViewModel() {
    /** null while a load is in flight; otherwise the outcome of the last one, with every route's badge read once. */
    private val loadResult = MutableStateFlow<Result<List<RouteItem>>?>(null)
    private val searchQuery = MutableStateFlow("")

    // Started eagerly so the combined state tracks the stored recents for as long as the
    // ViewModel lives, rather than being torn down and rebuilt around each subscriber.
    val uiState: StateFlow<RoutesUiState> = combine(
        loadResult,
        searchQuery,
        tripStateStore.recentRoutes,
    ) { result, query, recentIds ->
        when {
            result == null -> RoutesUiState.Loading
            result.isSuccess -> {
                val routes = result.getOrThrow()
                RoutesUiState.Loaded(
                    routes = routes,
                    visible = routes.filter { it.matches(query.trim().lowercase()) },
                    // Recents are a shortcut past the search, so they make way for one.
                    recent = if (query.isEmpty()) routes.matching(recentIds) else emptyList(),
                    query = query,
                    grid = showsAsGrid(routes),
                )
            }
            result.exceptionOrNull() is ApiError.CatalogUnavailable -> RoutesUiState.NoSchedule
            result.exceptionOrNull() is ApiError.Unauthorized -> RoutesUiState.SessionEnded
            else -> RoutesUiState.Error
        }
    }.stateIn(viewModelScope, SharingStarted.Eagerly, RoutesUiState.Loading)

    init {
        load()
    }

    fun retry() = load()

    fun onQueryChange(value: String) {
        searchQuery.value = value
    }

    private fun load() {
        loadResult.value = null
        viewModelScope.launch {
            val result = catalogRepository.routes().map { routes -> routes.map { it.toItem() } }
            // The client has already tried a refresh. The tokens go before the state changes, so nothing can read a dead
            // one once the session shows as ended.
            if (result.exceptionOrNull() is ApiError.Unauthorized) authRepository.endSessionLocally()
            loadResult.value = result
        }
    }
}

private fun RouteDto.toItem() = RouteItem(id, RouteBadgeModel.of(shortName, color, textColor), longName)

/**
 * A feed that names none of its routes, as King County's names none, is a list of bare numbers, which reads better
 * as a grid of badges. A feed with any long names keeps its rows, so a few unnamed routes do not make it uneven.
 */
internal fun showsAsGrid(routes: List<RouteItem>): Boolean = routes.none { it.longName.isNotBlank() }

/**
 * Prefix on the number, substring on the name — the rule `RoutesView` searches by, so a driver
 * typing "5" is offered route 5 and not route 15. [query] arrives trimmed and lowercased; an
 * empty one matches everything.
 */
private fun RouteItem.matches(query: String): Boolean =
    query.isEmpty() || badge.shortName.lowercase().startsWith(query) || longName.lowercase().contains(query)

/**
 * The routes named by [ids], in the order given, skipping any the catalog no longer has:
 * recently driven routes are remembered as bare ids, so the picker resolves them against what
 * is actually on offer.
 */
private fun List<RouteItem>.matching(ids: List<String>): List<RouteItem> =
    ids.mapNotNull { id -> firstOrNull { it.id == id } }
