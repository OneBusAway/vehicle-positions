package org.onebusaway.vehicletracker.data

import org.onebusaway.vehicletracker.data.api.EndTripRequest
import org.onebusaway.vehicletracker.data.api.StartTripRequest
import org.onebusaway.vehicletracker.data.api.TrackerApi
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import javax.inject.Inject

class TripRepository @Inject constructor(
    private val api: TrackerApi,
    private val tripStateStore: TripStateStore,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    suspend fun start(vehicleId: String, routeId: String, gtfsTripId: String): Result<ActiveTrip> = try {
        val trip = api.startTrip(StartTripRequest(vehicleId, routeId, gtfsTripId))
        val activeTrip = ActiveTrip(
            tripDbId = trip.id,
            locationTripId = gtfsTripId.ifBlank { routeId },
            vehicleId = vehicleId,
            routeId = routeId,
            startedAtEpochSec = clock(),
        )
        tripStateStore.saveActiveTrip(activeTrip)
        tripStateStore.addRecentRoute(routeId)
        Result.success(activeTrip)
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }

    suspend fun end(tripDbId: Long): Result<Unit> = try {
        api.endTrip(EndTripRequest(tripDbId))
        tripStateStore.clearActiveTrip()
        Result.success(Unit)
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }
}
