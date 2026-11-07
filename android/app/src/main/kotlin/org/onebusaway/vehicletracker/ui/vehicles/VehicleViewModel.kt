package org.onebusaway.vehicletracker.ui.vehicles

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
import org.onebusaway.vehicletracker.data.VehiclePrefsStore
import org.onebusaway.vehicletracker.data.VehicleRepository
import org.onebusaway.vehicletracker.data.api.VehicleDto
import javax.inject.Inject

/** Where a bus sits on the bus step. While the driver searches, every match is under [MATCHES]. */
enum class BusSection { FAVORITES, RECENT, ALL, MATCHES }

/**
 * One bus as its tile draws it: the fleet number big, and [label] under it, or null when the label is blank or
 * only repeats the number.
 */
@Immutable
data class BusTile(val id: String, val label: String?, val favorite: Boolean)

@Immutable
data class BusGroup(val section: BusSection, val tiles: List<BusTile>)

sealed interface VehiclesUiState {
    data object Loading : VehiclesUiState

    data class Loaded(
        /** Every vehicle assigned to the driver, unaffected by [query]. */
        val vehicles: List<VehicleDto>,
        val query: String,
        /** [vehicles] narrowed by [query] and put in display order, under their sections; empty sections are left out. */
        val groups: List<BusGroup>,
    ) : VehiclesUiState {
        /**
         * The vehicle to open directly, skipping the picker: a driver with exactly one
         * assignment has no choice to make. Derived from [vehicles] and never from [groups],
         * so a search that narrows to a single match does not navigate the driver into a trip
         * mid-keystroke.
         */
        val autoSelectId: String? get() = vehicles.singleOrNull()?.id
    }

    data object Error : VehiclesUiState

    /** The server has ended the session and the tokens are gone, so the screen opens Sign in. */
    data object SessionEnded : VehiclesUiState
}

@HiltViewModel
class VehicleViewModel @Inject constructor(
    private val vehicleRepository: VehicleRepository,
    private val vehiclePrefsStore: VehiclePrefsStore,
    private val authRepository: AuthRepository,
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
                    query = query,
                    groups = groupsOf(vehicles, query, favorites, recents),
                )
            }
            result.exceptionOrNull() is ApiError.Unauthorized -> VehiclesUiState.SessionEnded
            else -> VehiclesUiState.Error
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
        viewModelScope.launch {
            val result = vehicleRepository.myVehicles()
            // The client has already tried a refresh. The tokens go before the state changes, so nothing can read a dead
            // one once the session shows as ended.
            if (result.exceptionOrNull() is ApiError.Unauthorized) authRepository.endSessionLocally()
            loadResult.value = result
        }
    }
}

/**
 * Case-insensitive substring match on id or label, mirroring the ILIKE search the admin vehicle
 * list runs server-side. An empty query matches everything.
 */
private fun VehicleDto.matches(query: String): Boolean =
    query.isEmpty() || id.contains(query, ignoreCase = true) || label.contains(query, ignoreCase = true)

/**
 * Favorites, then the recently used ones that are not favorites, then the rest, each bus in one section only. While
 * the driver searches, the matches go under one section in the same order.
 */
private fun groupsOf(
    vehicles: List<VehicleDto>,
    query: String,
    favorites: Set<String>,
    recents: List<String>,
): List<BusGroup> {
    fun group(section: BusSection, list: List<VehicleDto>) =
        list.takeIf { it.isNotEmpty() }?.let { matched -> BusGroup(section, matched.map { it.toTile(it.id in favorites) }) }

    val ordered = orderForDisplay(vehicles.filter { it.matches(query) }, favorites, recents)
    if (query.isNotEmpty()) return listOfNotNull(group(BusSection.MATCHES, ordered))
    val (favorite, rest) = ordered.partition { it.id in favorites }
    val (recent, others) = rest.partition { it.id in recents }
    return listOfNotNull(
        group(BusSection.FAVORITES, favorite),
        group(BusSection.RECENT, recent),
        group(BusSection.ALL, others),
    )
}

private fun VehicleDto.toTile(favorite: Boolean) =
    BusTile(id = id, label = label.takeIf { it.isNotBlank() && it != id }, favorite = favorite)

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
