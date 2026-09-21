package org.onebusaway.vehicletracker.ui.routes

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
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.api.RouteDto
import javax.inject.Inject

sealed interface RoutesUiState {
    data object Loading : RoutesUiState

    data class Loaded(
        /** Every route in the catalog, unaffected by [query]. */
        val routes: List<RouteDto>,
        /** [routes] narrowed by [query]. */
        val visible: List<RouteDto>,
        /** Recently driven routes, newest first; empty while the driver is searching. */
        val recent: List<RouteDto>,
        val query: String,
    ) : RoutesUiState

    data class Error(val retry: Boolean) : RoutesUiState
}

@HiltViewModel
class RoutesViewModel @Inject constructor(
    private val catalogRepository: CatalogRepository,
    tripStateStore: TripStateStore,
) : ViewModel() {
    /** null while a load is in flight; otherwise the outcome of the last one. */
    private val loadResult = MutableStateFlow<Result<List<RouteDto>>?>(null)
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
                )
            }
            else -> RoutesUiState.Error(retry = result.exceptionOrNull() !is ApiError.Unauthorized)
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
        viewModelScope.launch { loadResult.value = catalogRepository.routes() }
    }
}

/**
 * Prefix on the number, substring on the name — the rule `RoutesView` searches by, so a driver
 * typing "5" is offered route 5 and not route 15. [query] arrives trimmed and lowercased; an
 * empty one matches everything.
 */
private fun RouteDto.matches(query: String): Boolean =
    query.isEmpty() || shortName.lowercase().startsWith(query) || longName.lowercase().contains(query)

/**
 * The routes named by [ids], in the order given, skipping any the catalog no longer has:
 * recently driven routes are remembered as bare ids, so the picker resolves them against what
 * is actually on offer.
 */
private fun List<RouteDto>.matching(ids: List<String>): List<RouteDto> =
    ids.mapNotNull { id -> firstOrNull { it.id == id } }
