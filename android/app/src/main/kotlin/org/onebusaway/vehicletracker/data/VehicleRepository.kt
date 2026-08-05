package org.onebusaway.vehicletracker.data

import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.api.VehicleDto
import javax.inject.Inject

class VehicleRepository @Inject constructor(
    private val apiProvider: TrackerApiProvider,
) {
    // apiProvider.get() is called here (not injected as a resolved TrackerApi) so that a missing
    // server URL (e.g. cold start racing session restore) surfaces as Result.failure instead of
    // an uncaught exception during construction.
    suspend fun myVehicles(): Result<List<VehicleDto>> = try {
        Result.success(apiProvider.get().myVehicles())
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }
}
