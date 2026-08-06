package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.flow.MutableStateFlow
import org.onebusaway.vehicletracker.service.ServiceController

class FakeSessionStore : SessionStore {
    val state = MutableStateFlow(Session(null, null, null))
    override val session = state
    override suspend fun saveLogin(serverUrl: String, token: String, issuedAtEpochSec: Long) {
        state.value = Session(serverUrl, token, issuedAtEpochSec)
    }
    override suspend fun clearToken() { state.value = state.value.copy(token = null, issuedAtEpochSec = null) }
}

class FakeTripStateStore : TripStateStore {
    val tripState = MutableStateFlow<ActiveTrip?>(null)
    val routesState = MutableStateFlow<List<String>>(emptyList())
    override val activeTrip = tripState
    override val recentRoutes = routesState
    override suspend fun saveActiveTrip(trip: ActiveTrip) { tripState.value = trip }
    override suspend fun clearActiveTrip() { tripState.value = null }
    override suspend fun addRecentRoute(routeId: String) {
        routesState.value = (listOf(routeId) + routesState.value.filter { it != routeId }).take(5)
    }
}

class FakeServiceController : ServiceController {
    var startCount = 0
    var stopCount = 0
    override fun startTracking() { startCount++ }
    override fun stopTracking() { stopCount++ }
}
