package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.map
import org.onebusaway.vehicletracker.data.TrackingState

/**
 * Calls [startTracking] when collection starts with tracking active, and again each time it turns
 * active. Going inactive never calls it, so a trip the driver ended is not restarted. Calling it on
 * a healthy service is harmless: [LocationTrackingService] only requests location updates when it
 * has none running.
 */
internal suspend fun rearmWhenActive(state: Flow<TrackingState>, startTracking: () -> Unit) {
    state.map { it.active }.distinctUntilChanged().filter { it }.collect { startTracking() }
}
