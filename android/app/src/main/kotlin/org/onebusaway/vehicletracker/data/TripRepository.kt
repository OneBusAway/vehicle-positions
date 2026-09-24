package org.onebusaway.vehicletracker.data

import android.util.Log
import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.EndTripRequest
import org.onebusaway.vehicletracker.data.api.StartTripRequest
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import javax.inject.Inject

private const val TAG = "TripRepository"

class TripRepository @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val tripStateStore: TripStateStore,
    private val vehiclePrefsStore: VehiclePrefsStore,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    /**
     * [serviceDate] is the catalog's own `service_date` for the run, YYYYMMDD, and not the
     * device's calendar date: a run scheduled past midnight belongs to the service date before
     * it, and a phone in another timezone can be a day out besides. It is what the feed's
     * `TripDescriptor.start_date` ends up carrying.
     *
     * `apiProvider.get()` is called here (not injected as a resolved `TrackerApi`) so that a
     * missing server URL — a cold start racing session restore — surfaces as `Result.failure`
     * instead of an uncaught exception during construction.
     */
    suspend fun start(
        vehicleId: String,
        routeId: String,
        gtfsTripId: String,
        serviceDate: String,
    ): Result<ActiveTrip> = try {
        val cleanedTripId = gtfsTripId.trim()
        val cleanedRouteId = routeId.trim()
        val trip = apiProvider.get().startTrip(StartTripRequest(vehicleId, cleanedRouteId, cleanedTripId))
        val startedAt = clock()
        val activeTrip = ActiveTrip(
            tripDbId = trip.id,
            gtfsTripId = cleanedTripId,
            vehicleId = vehicleId,
            routeId = cleanedRouteId,
            startDate = serviceDate,
            startedAtEpochSec = startedAt,
        )
        tripStateStore.saveActiveTrip(activeTrip)
        recordLocally("recent route") { tripStateStore.addRecentRoute(cleanedRouteId) }
        // Recorded here rather than when the driver taps a vehicle: a tap they back out of
        // is not a use, and recents full of abandoned taps make the picker worse.
        recordLocally("recent vehicle") { vehiclePrefsStore.recordUse(vehicleId) }
        Result.success(activeTrip)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }

    /**
     * Runs a local bookkeeping write that must not be allowed to fail the trip. By the time
     * these run the trip is already active on the server and saved locally, so letting a
     * DataStore error reach the caller's catch would report a started trip as a network
     * failure — and the driver's retry would come back 409. Cancellation still propagates.
     */
    private suspend fun recordLocally(what: String, write: suspend () -> Unit) {
        try {
            write()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Log.w(TAG, "Could not record $what", e)
        }
    }

    suspend fun end(tripDbId: Long): Result<Unit> = try {
        apiProvider.get().endTrip(EndTripRequest(tripDbId))
        tripStateStore.clearActiveTrip()
        Result.success(Unit)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }
}
