package org.onebusaway.vehiclepositions.util

import android.util.Log
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import javax.inject.Inject
import javax.inject.Singleton

sealed class ServiceEvent {
    object StopShift : ServiceEvent()
    object NavigateToLogin : ServiceEvent()
    object LocationPermissionRevoked : ServiceEvent()
}

@Singleton
class ServiceEventBus @Inject constructor() {

    private val _events = MutableSharedFlow<ServiceEvent>(extraBufferCapacity = 4)
    val events: SharedFlow<ServiceEvent> = _events.asSharedFlow()

    fun emit(event: ServiceEvent) {
        val emitted = _events.tryEmit(event)
        if (!emitted) {
            // buffer full - critical navigation/security event dropped
            // thiss should not happen in normal operation
            Log.e("ServiceEventBus", "Event dropped — buffer full: $event")
        }
    }

    fun emitStopShift() = emit(ServiceEvent.StopShift)
    fun emitNavigateToLogin() = emit(ServiceEvent.NavigateToLogin)
    fun emitLocationPermissionRevoked() = emit(ServiceEvent.LocationPermissionRevoked)
}