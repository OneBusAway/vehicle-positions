package org.onebusaway.vehicletracker.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

data class ActiveTrip(
    val tripDbId: Long,
    val locationTripId: String,
    val vehicleId: String,
    val routeId: String,
    val startedAtEpochSec: Long,
)

interface TripStateStore {
    val activeTrip: Flow<ActiveTrip?>
    val recentRoutes: Flow<List<String>>
    suspend fun saveActiveTrip(trip: ActiveTrip)
    suspend fun clearActiveTrip()
    suspend fun addRecentRoute(routeId: String)
}

private val Context.tripStateDataStore by preferencesDataStore(name = "trip_state")

class DataStoreTripStateStore(private val context: Context) : TripStateStore {
    private object Keys {
        val TRIP_DB_ID = longPreferencesKey("trip_db_id")
        val TRIP_LOCATION_ID = stringPreferencesKey("trip_location_id")
        val TRIP_VEHICLE_ID = stringPreferencesKey("trip_vehicle_id")
        val TRIP_ROUTE_ID = stringPreferencesKey("trip_route_id")
        val TRIP_STARTED_AT = longPreferencesKey("trip_started_at")
        val RECENT_ROUTES = stringPreferencesKey("recent_routes")
    }

    override val activeTrip: Flow<ActiveTrip?> = context.tripStateDataStore.data.map { prefs ->
        val tripDbId = prefs[Keys.TRIP_DB_ID]
        val locationTripId = prefs[Keys.TRIP_LOCATION_ID]
        val vehicleId = prefs[Keys.TRIP_VEHICLE_ID]
        val routeId = prefs[Keys.TRIP_ROUTE_ID]
        val startedAt = prefs[Keys.TRIP_STARTED_AT]
        if (tripDbId != null && locationTripId != null && vehicleId != null && routeId != null && startedAt != null) {
            ActiveTrip(tripDbId, locationTripId, vehicleId, routeId, startedAt)
        } else {
            null
        }
    }

    override val recentRoutes: Flow<List<String>> = context.tripStateDataStore.data.map { prefs ->
        prefs[Keys.RECENT_ROUTES]?.split("|")?.filter { it.isNotEmpty() } ?: emptyList()
    }

    override suspend fun saveActiveTrip(trip: ActiveTrip) {
        context.tripStateDataStore.edit { prefs ->
            prefs[Keys.TRIP_DB_ID] = trip.tripDbId
            prefs[Keys.TRIP_LOCATION_ID] = trip.locationTripId
            prefs[Keys.TRIP_VEHICLE_ID] = trip.vehicleId
            prefs[Keys.TRIP_ROUTE_ID] = trip.routeId
            prefs[Keys.TRIP_STARTED_AT] = trip.startedAtEpochSec
        }
    }

    override suspend fun clearActiveTrip() {
        context.tripStateDataStore.edit { prefs ->
            prefs.remove(Keys.TRIP_DB_ID)
            prefs.remove(Keys.TRIP_LOCATION_ID)
            prefs.remove(Keys.TRIP_VEHICLE_ID)
            prefs.remove(Keys.TRIP_ROUTE_ID)
            prefs.remove(Keys.TRIP_STARTED_AT)
        }
    }

    override suspend fun addRecentRoute(routeId: String) {
        val cleaned = routeId.replace("|", "")
        context.tripStateDataStore.edit { prefs ->
            val current = prefs[Keys.RECENT_ROUTES]?.split("|")?.filter { it.isNotEmpty() } ?: emptyList()
            val updated = (listOf(cleaned) + current.filter { it != cleaned }).take(5)
            prefs[Keys.RECENT_ROUTES] = updated.joinToString("|")
        }
    }
}
