package org.onebusaway.vehiclepositions.util

import android.location.Location
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class LocationStateHolder @Inject constructor() {
    private val _lastLocation = MutableStateFlow<Location?>(null)
    val lastLocation: StateFlow<Location?> = _lastLocation.asStateFlow()

    fun updateLocation(location: Location) {
        _lastLocation.value = location
    }

    fun hasLocation(): Boolean = _lastLocation.value != null
}