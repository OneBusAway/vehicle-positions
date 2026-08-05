package org.onebusaway.vehicletracker.data

import org.onebusaway.vehicletracker.data.api.TrackerApi
import org.onebusaway.vehicletracker.data.api.VehicleDto
import javax.inject.Inject

class VehicleRepository @Inject constructor(
    private val api: TrackerApi,
) {
    suspend fun myVehicles(): Result<List<VehicleDto>> = try {
        Result.success(api.myVehicles())
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }
}
