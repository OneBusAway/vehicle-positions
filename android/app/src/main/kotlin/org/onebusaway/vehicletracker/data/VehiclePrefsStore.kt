package org.onebusaway.vehicletracker.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * On-device vehicle-picker preferences: the vehicles a driver starred, and the ones they last
 * drove. Kept separate from [TripStateStore] — that interface is named for trip state and
 * already carries six members. Never synced to the server (#36).
 */
interface VehiclePrefsStore {
    val favorites: Flow<Set<String>>
    val recents: Flow<List<String>>
    suspend fun toggleFavorite(vehicleId: String)
    suspend fun recordUse(vehicleId: String)
}

/** Internal, not private, only because [org.onebusaway.vehicletracker.di.AppModule] provides the store. */
internal val Context.vehiclePrefsDataStore: DataStore<Preferences> by preferencesDataStore(name = "vehicle_prefs")

/** Matches the recent-route cap in [DataStoreTripStateStore.addRecentRoute]. */
private const val MAX_RECENT_VEHICLES = 5

private const val DELIMITER = "|"

class DataStoreVehiclePrefsStore(private val dataStore: DataStore<Preferences>) : VehiclePrefsStore {
    private object Keys {
        val FAVORITE_VEHICLES = stringPreferencesKey("favorite_vehicles")
        val RECENT_VEHICLES = stringPreferencesKey("recent_vehicles")
    }

    override val favorites: Flow<Set<String>> = dataStore.data.map { prefs ->
        decode(prefs[Keys.FAVORITE_VEHICLES]).toSet()
    }

    override val recents: Flow<List<String>> = dataStore.data.map { prefs ->
        decode(prefs[Keys.RECENT_VEHICLES])
    }

    override suspend fun toggleFavorite(vehicleId: String) {
        val cleaned = sanitize(vehicleId)
        dataStore.edit { prefs ->
            val current = decode(prefs[Keys.FAVORITE_VEHICLES]).toSet()
            val updated = if (cleaned in current) current - cleaned else current + cleaned
            prefs[Keys.FAVORITE_VEHICLES] = updated.joinToString(DELIMITER)
        }
    }

    override suspend fun recordUse(vehicleId: String) {
        val cleaned = sanitize(vehicleId)
        dataStore.edit { prefs ->
            val current = decode(prefs[Keys.RECENT_VEHICLES])
            val updated = (listOf(cleaned) + current.filter { it != cleaned }).take(MAX_RECENT_VEHICLES)
            prefs[Keys.RECENT_VEHICLES] = updated.joinToString(DELIMITER)
        }
    }

    private fun decode(stored: String?): List<String> =
        stored?.split(DELIMITER)?.filter { it.isNotEmpty() } ?: emptyList()

    /**
     * Strips the list delimiter from an id before storing it, as
     * [DataStoreTripStateStore.addRecentRoute] does: an id containing one would decode back as
     * two entries. The server's vehicle id pattern (`^[a-zA-Z0-9._-]+$`) makes that impossible
     * today, but that constraint lives server-side where this store cannot see it, so the strip
     * stays as a cheap guard on the encoding.
     */
    private fun sanitize(vehicleId: String): String = vehicleId.replace(DELIMITER, "")
}
