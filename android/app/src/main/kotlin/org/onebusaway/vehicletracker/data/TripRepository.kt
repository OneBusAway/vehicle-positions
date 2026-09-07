package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.EndTripRequest
import org.onebusaway.vehicletracker.data.api.StartTripRequest
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import java.time.ZoneId
import javax.inject.Inject

class TripRepository @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val tripStateStore: TripStateStore,
    @param:EpochSecondsClock private val clock: () -> Long,
    private val zone: ZoneId,
) {
    // apiProvider.get() is called here (not injected as a resolved TrackerApi) so that a missing
    // server URL (e.g. cold start racing session restore) surfaces as Result.failure instead of
    // an uncaught exception during construction.
    suspend fun start(vehicleId: String, routeId: String, gtfsTripId: String): Result<ActiveTrip> = try {
        val cleanedTripId = gtfsTripId.trim()
        val cleanedRouteId = routeId.trim()
        val trip = apiProvider.get().startTrip(StartTripRequest(vehicleId, cleanedRouteId, cleanedTripId))
        val startedAt = clock()
        val activeTrip = ActiveTrip(
            tripDbId = trip.id,
            gtfsTripId = cleanedTripId,
            vehicleId = vehicleId,
            routeId = cleanedRouteId,
            startDate = serviceDate(startedAt, zone),
            startedAtEpochSec = startedAt,
        )
        tripStateStore.saveActiveTrip(activeTrip)
        tripStateStore.addRecentRoute(cleanedRouteId)
        Result.success(activeTrip)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
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
