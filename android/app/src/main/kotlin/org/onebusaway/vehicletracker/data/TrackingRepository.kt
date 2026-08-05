package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

enum class TrackingProblem { NONE, NO_NETWORK, NO_GPS, AUTH_EXPIRED, CLOCK_SKEW }

data class TrackingState(
    val active: Boolean = false,
    val problem: TrackingProblem = TrackingProblem.NONE,
    val fixesSent: Int = 0,
    val tripStartedAtEpochSec: Long? = null,
)

@Singleton
class TrackingRepository @Inject constructor() {
    private val _state = MutableStateFlow(TrackingState())
    val state: StateFlow<TrackingState> = _state.asStateFlow()
    fun update(transform: (TrackingState) -> TrackingState) = _state.update(transform)
}
