package org.onebusaway.vehicletracker.service

import javax.inject.Inject

/**
 * Controls the lifecycle of the background location-tracking service.
 * Implemented as a no-op for now; a real foreground-service-backed implementation
 * is bound in Task 8.
 */
interface ServiceController {
    fun startTracking()
    fun stopTracking()
}

class NoOpServiceController @Inject constructor() : ServiceController {
    override fun startTracking() = Unit
    override fun stopTracking() = Unit
}
