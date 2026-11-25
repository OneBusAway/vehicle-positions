package org.onebusaway.vehicletracker.ui.tracking

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.OccupancyStatus
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.WaitingReports
import org.onebusaway.vehicletracker.data.map.MapFileSource
import org.onebusaway.vehicletracker.data.map.MapFileState
import java.io.IOException
import javax.inject.Inject

private const val TAG = "TrackingViewModel"

data class TrackingUiState(
    val tracking: TrackingState = TrackingState(),
    val activeTrip: ActiveTrip? = null,
    val ending: Boolean = false,
    val endTripError: Boolean = false,
    val mapFile: MapFileState = MapFileState.None,
    /** The trip has ended, and the screen goes on to choose the next one. */
    val ended: Boolean = false,
    /** Reports saved while there was no connection, still on the phone; null when there are none. */
    val waiting: ReportsWaiting? = null,
)

@HiltViewModel
class TrackingViewModel @Inject constructor(
    private val trackingRepository: TrackingRepository,
    private val tripStateStore: TripStateStore,
    private val tripEnder: TripEnder,
    mapFiles: MapFileSource,
    waitingReports: WaitingReports,
) : ViewModel() {
    // Starts from what the repositories have, as the stored trip is still to be read: a problem there already is not one
    // that comes up once the screen has opened, and the map is drawn once, with its file.
    private val _uiState = MutableStateFlow(TrackingUiState(tracking = trackingRepository.state.value, mapFile = mapFiles.state.value))
    val uiState: StateFlow<TrackingUiState> = _uiState.asStateFlow()

    init {
        combine(
            trackingRepository.state,
            tripStateStore.activeTrip,
            tripEnder.state,
            mapFiles.state,
            waitingReports.waiting,
        ) { tracking, trip, end, mapFile, waiting ->
            TrackingUiState(
                tracking = tracking,
                activeTrip = trip,
                ending = end.ending,
                endTripError = end.failed,
                mapFile = mapFile,
                ended = end.ended,
                waiting = waiting,
            )
        }
            .onEach { _uiState.value = it }
            .launchIn(viewModelScope)
    }

    fun onEndTrip() {
        val trip = _uiState.value.activeTrip ?: return
        viewModelScope.launch { tripEnder.end(trip) }
    }

    fun onEndTripLocally() {
        viewModelScope.launch { tripEnder.endLocally() }
    }

    fun dismissEndTripError() = tripEnder.dismissError()

    /** The screen has gone on from the ended trip, so one recreated later does not go on again. */
    fun onNavigated() = tripEnder.onNavigated()

    /**
     * The driver tapped [tapped] in the occupancy row: it becomes the trip's, or is cleared when it already is. Read
     * against what the screen shows, which is what the driver tapped.
     */
    fun onOccupancyClick(tapped: OccupancyStatus) {
        val trip = _uiState.value.activeTrip ?: return
        val occupancy = tapped.takeUnless { it == trip.occupancy }
        viewModelScope.launch {
            try {
                tripStateStore.setOccupancy(trip.tripDbId, occupancy)
            } catch (e: IOException) {
                // A full disk, say. The row shows what is stored, so the driver sees that the tap did not take.
                AppLog.w(TAG, "Could not store the occupancy", e)
            }
        }
    }

    /** The tracking service takes a fix every second only while this screen is there to show it. */
    fun onScreenVisible(visible: Boolean) = trackingRepository.setLiveView(visible)
}
