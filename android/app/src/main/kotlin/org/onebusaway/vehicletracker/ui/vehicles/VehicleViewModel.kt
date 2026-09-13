package org.onebusaway.vehicletracker.ui.vehicles

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
import org.onebusaway.vehicletracker.data.VehiclePrefsStore
import org.onebusaway.vehicletracker.data.VehicleRepository
import org.onebusaway.vehicletracker.data.api.VehicleDto
import javax.inject.Inject

sealed interface VehiclesUiState {
    data object Loading : VehiclesUiState

    data class Loaded(
        /** Every vehicle assigned to the driver, unaffected by [query]. */
        val vehicles: List<VehicleDto>,
        /** [vehicles] narrowed by [query] and put in display order. */
        val visible: List<VehicleDto>,
        val query: String,
        val favorites: Set<String>,
    ) : VehiclesUiState {
        /**
         * The vehicle to open directly, skipping the picker: a driver with exactly one
         * assignment has no choice to make. Derived from [vehicles] and never from [visible],
         * so a search that narrows to a single match does not navigate the driver into a trip
         * mid-keystroke.
         */
        val autoSelectId: String? get() = vehicles.singleOrNull()?.id
    }

    data class Error(val retry: Boolean) : VehiclesUiState
}

@HiltViewModel
class VehicleViewModel @Inject constructor(
    private val vehicleRepository: VehicleRepository,
    private val vehiclePrefsStore: VehiclePrefsStore,
) : ViewModel() {
    /** null while a load is in flight; otherwise the outcome of the last one. */
    private val loadResult = MutableStateFlow<Result<List<VehicleDto>>?>(null)
    private val searchQuery = MutableStateFlow("")

    // Started eagerly so the combined state tracks the stored favorites and recents for as long
    // as the ViewModel lives, rather than being torn down and rebuilt around each subscriber.
    val uiState: StateFlow<VehiclesUiState> = combine(
        loadResult,
        searchQuery,
        vehiclePrefsStore.favorites,
        vehiclePrefsStore.recents,
    ) { result, query, favorites, recents ->
        when {
            result == null -> VehiclesUiState.Loading
            result.isSuccess -> {
                val vehicles = result.getOrThrow()
                VehiclesUiState.Loaded(
                    vehicles = vehicles,
                    visible = orderForDisplay(vehicles.filter { it.matches(query) }, favorites, recents),
                    query = query,
                    favorites = favorites,
                )
            }
            else -> VehiclesUiState.Error(retry = result.exceptionOrNull() !is ApiError.Unauthorized)
        }
    }.stateIn(viewModelScope, SharingStarted.Eagerly, VehiclesUiState.Loading)

    init {
        load()
    }

    fun retry() = load()

    fun onQueryChange(value: String) {
        searchQuery.value = value
    }

    fun onToggleFavorite(vehicleId: String) {
        viewModelScope.launch { vehiclePrefsStore.toggleFavorite(vehicleId) }
    }

    private fun load() {
        loadResult.value = null
        viewModelScope.launch { loadResult.value = vehicleRepository.myVehicles() }
    }
}

/**
 * Case-insensitive substring match on id or label, mirroring the ILIKE search the admin vehicle
 * list runs server-side. An empty query matches everything.
 */
private fun VehicleDto.matches(query: String): Boolean =
    query.isEmpty() || id.contains(query, ignoreCase = true) || label.contains(query, ignoreCase = true)

private const val GROUP_FAVORITE = 0
private const val GROUP_RECENT = 1
private const val GROUP_OTHER = 2

/**
 * Favorites first, then recently used newest-first, then everything else, with label and then id
 * breaking ties. The comparator is deliberately a total order: one that left ties unresolved
 * would let the list reshuffle between recompositions and make its tests flaky.
 */
private fun orderForDisplay(
    vehicles: List<VehicleDto>,
    favorites: Set<String>,
    recents: List<String>,
): List<VehicleDto> {
    fun group(vehicle: VehicleDto) = when {
        vehicle.id in favorites -> GROUP_FAVORITE
        vehicle.id in recents -> GROUP_RECENT
        else -> GROUP_OTHER
    }

    // recents is newest-first, so a vehicle's index in it is its recency rank. The key is
    // constant outside GROUP_RECENT, which leaves label and id to order the other two groups.
    fun recency(vehicle: VehicleDto) =
        if (group(vehicle) == GROUP_RECENT) recents.indexOf(vehicle.id) else 0

    return vehicles.sortedWith(
        compareBy<VehicleDto> { group(it) }
            .thenBy { recency(it) }
            .thenBy { it.label }
            .thenBy { it.id },
    )
}
