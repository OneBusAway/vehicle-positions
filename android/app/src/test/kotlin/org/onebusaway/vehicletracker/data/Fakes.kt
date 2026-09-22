package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.flow.MutableStateFlow
import org.onebusaway.vehicletracker.service.ServiceController
import java.security.GeneralSecurityException

/**
 * Stands in for [KeystoreCryptor] so session and migration logic runs on the JVM, where CI
 * actually executes it.
 *
 * It reverses the value rather than just tagging it, so a test can assert that the plaintext
 * token never appears in the bytes on disk, and [decrypt] rejects anything it did not produce,
 * so a token written in the clear cannot read back as if it had been encrypted.
 */
class FakeCryptor : Cryptor {
    var failEncrypt = false
    var failDecrypt = false

    override fun encrypt(plaintext: String): String {
        if (failEncrypt) throw GeneralSecurityException("keystore unavailable")
        return PREFIX + plaintext.reversed()
    }

    override fun decrypt(ciphertext: String): String {
        if (failDecrypt) throw GeneralSecurityException("key no longer usable")
        if (!ciphertext.startsWith(PREFIX)) throw GeneralSecurityException("not ciphertext")
        return ciphertext.removePrefix(PREFIX).reversed()
    }

    private companion object {
        const val PREFIX = "enc:"
    }
}

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

class FakeVehiclePrefsStore : VehiclePrefsStore {
    val favoritesState = MutableStateFlow<Set<String>>(emptySet())
    val recentsState = MutableStateFlow<List<String>>(emptyList())
    override val favorites = favoritesState
    override val recents = recentsState
    override suspend fun toggleFavorite(vehicleId: String) {
        val current = favoritesState.value
        favoritesState.value = if (vehicleId in current) current - vehicleId else current + vehicleId
    }
    override suspend fun recordUse(vehicleId: String) {
        recentsState.value = (listOf(vehicleId) + recentsState.value.filter { it != vehicleId }).take(5)
    }
}

class FakeServiceController : ServiceController {
    var startCount = 0
    var stopCount = 0
    override fun startTracking() { startCount++ }
    override fun stopTracking() { stopCount++ }
}
