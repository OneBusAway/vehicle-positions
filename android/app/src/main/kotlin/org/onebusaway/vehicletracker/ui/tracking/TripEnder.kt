package org.onebusaway.vehicletracker.ui.tracking

import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.service.ServiceController
import javax.inject.Inject

private const val TAG = "TripEnder"

data class EndTripState(val ending: Boolean = false, val failed: Boolean = false)

/**
 * Ends the active trip for every screen that offers to: the tracking screen and the prompt after
 * an interrupted shift. One implementation, so an end the server does not confirm is handled the
 * same way from either: retry, or end on this device only.
 *
 * Unscoped, so each ViewModel gets its own and one screen's failed end never shows on another.
 */
class TripEnder @Inject constructor(
    private val tripRepository: TripRepository,
    private val tripStateStore: TripStateStore,
    private val tripGeometryStore: TripGeometryStore,
    private val serviceController: ServiceController,
    private val sessionStore: SessionStore,
) {
    private val _state = MutableStateFlow(EndTripState())
    val state: StateFlow<EndTripState> = _state.asStateFlow()

    suspend fun end(trip: ActiveTrip, onEnded: () -> Unit) {
        if (_state.value.ending) return
        _state.value = EndTripState(ending = true)
        // The server ends a trip only for the driver who started it. It answers anyone else 404, which reads as a trip
        // already ended and would clear one that is still open. Another driver's trip is ended on this phone instead,
        // which keeps its end for its own driver.
        if (startedByAnotherDriver(trip)) {
            endOnThisPhone(onEnded)
            return
        }
        tripRepository.end(trip.tripDbId).fold(
            onSuccess = {
                tripGeometryStore.clear()
                serviceController.stopTracking()
                _state.value = EndTripState()
                onEnded()
            },
            onFailure = {
                _state.value = EndTripState(failed = true)
            },
        )
    }

    /**
     * The server still has the trip open; [TripRepository.start] sends its end before its driver's
     * next start. The store keeps the end under the driver who started the trip, and under the
     * driver signed in now only for a trip saved before drivers were recorded.
     */
    suspend fun endLocally(onEnded: () -> Unit) {
        // As for [end]: a second tap while the first is still writing would leave the screen twice.
        if (_state.value.ending) return
        _state.update { it.copy(ending = true) }
        endOnThisPhone(onEnded)
    }

    private suspend fun endOnThisPhone(onEnded: () -> Unit) {
        try {
            tripStateStore.endActiveTripLocally(sessionStore.session.first().driverId)
            tripGeometryStore.clear()
            serviceController.stopTracking()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Nothing above this catches it, so thrown on it would crash the app. The buttons come back, with the
            // failed end shown.
            Log.w(TAG, "Could not end the trip on this phone", e)
            _state.value = EndTripState(failed = true)
            return
        }
        _state.value = EndTripState()
        onEnded()
    }

    /**
     * Whether a driver is signed in who is not the one who started [trip]. Not for a trip saved before drivers were
     * recorded, nor with nobody signed in: both go to the server as before.
     */
    private suspend fun startedByAnotherDriver(trip: ActiveTrip): Boolean {
        val startedBy = trip.driverId
        val signedIn = sessionStore.session.first().driverId
        return startedBy != null && signedIn != null && startedBy != signedIn
    }

    fun dismissError() = _state.update { it.copy(failed = false) }
}
