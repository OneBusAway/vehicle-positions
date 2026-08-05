package org.onebusaway.vehicletracker.service

/**
 * Controls the lifecycle of the background location-tracking service.
 * Backed by [ServiceControllerImpl] (Task 8), bound in `di/AppModule.kt`.
 */
interface ServiceController {
    fun startTracking()
    fun stopTracking()
}
