package org.onebusaway.vehiclepositions.util

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Singleton event bus for one-way communication from LocationForegroundService
 * to ActiveTrackingViewModel. The service runs in a different coroutine scope
 * than the ViewModel so we use a SharedFlow as the bridge.
 */
sealed class ServiceEvent {
    data class NavigateToLogin(val message: String) : ServiceEvent()
    data class LocationPermissionRevoked(val message: String) : ServiceEvent()
}

@Singleton
class ServiceEventBus @Inject constructor() {
    private val _events = MutableSharedFlow<ServiceEvent>(extraBufferCapacity = 4)
    val events: SharedFlow<ServiceEvent> = _events.asSharedFlow()

    fun emit(event: ServiceEvent) {
        _events.tryEmit(event)
    }
}