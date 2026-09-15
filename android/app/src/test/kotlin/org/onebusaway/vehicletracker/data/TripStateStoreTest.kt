package org.onebusaway.vehicletracker.data

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.time.ZoneId

/**
 * Exercises [DataStoreTripStateStore] against a real file-backed DataStore, including trips
 * written by the build before gtfsTripId/startDate were split out of `trip_location_id`.
 */
class TripStateStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val scopes = mutableListOf<CoroutineScope>()
    private val nairobi = ZoneId.of("Africa/Nairobi")

    @After fun tearDown() = scopes.forEach { it.cancel() }

    /** DataStore allows one live instance per file, so every test gets its own file and scope. */
    private fun newDataStore(): DataStore<Preferences> = PreferenceDataStoreFactory.create(
        scope = CoroutineScope(Job() + Dispatchers.IO).also { scopes += it },
        produceFile = { File(tempFolder.newFolder(), "trip_state.preferences_pb") },
    )

    /** Writes the keys exactly as the previous app build did. */
    private suspend fun seedLegacyTrip(dataStore: DataStore<Preferences>, locationTripId: String, routeId: String) {
        dataStore.edit { prefs ->
            prefs[longPreferencesKey("trip_db_id")] = 7L
            prefs[stringPreferencesKey("trip_location_id")] = locationTripId
            prefs[stringPreferencesKey("trip_vehicle_id")] = "bus-1"
            prefs[stringPreferencesKey("trip_route_id")] = routeId
            // 2026-08-04T23:30:00Z, already 2026-08-05 in Nairobi.
            prefs[longPreferencesKey("trip_started_at")] = 1_785_886_200L
        }
    }

    @Test fun `a saved trip reads back unchanged`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)
        val trip = ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L)

        store.saveActiveTrip(trip)

        assertEquals(trip, store.activeTrip.first())
    }

    @Test fun `a legacy trip keeps the GTFS trip id the driver entered`() = runTest {
        val dataStore = newDataStore()
        seedLegacyTrip(dataStore, locationTripId = "trip-0830", routeId = "5")

        val trip = DataStoreTripStateStore(dataStore, nairobi).activeTrip.first()

        assertEquals(ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260805", 1_785_886_200L), trip)
    }

    @Test fun `a legacy route-only trip does not report the route as a trip id`() = runTest {
        val dataStore = newDataStore()
        seedLegacyTrip(dataStore, locationTripId = "5", routeId = "5")

        val trip = DataStoreTripStateStore(dataStore, nairobi).activeTrip.first()!!

        assertEquals("", trip.gtfsTripId)
        assertEquals("5", trip.routeId)
    }

    @Test fun `legacy ids are trimmed because the old build never trimmed them`() = runTest {
        val dataStore = newDataStore()
        seedLegacyTrip(dataStore, locationTripId = " trip-0830 ", routeId = "5 ")

        val trip = DataStoreTripStateStore(dataStore, nairobi).activeTrip.first()!!

        assertEquals("trip-0830", trip.gtfsTripId)
        assertEquals("5", trip.routeId)
    }

    @Test fun `clearing a legacy trip removes the legacy key too`() = runTest {
        val dataStore = newDataStore()
        seedLegacyTrip(dataStore, locationTripId = "trip-0830", routeId = "5")
        val store = DataStoreTripStateStore(dataStore, nairobi)

        store.clearActiveTrip()

        assertNull(store.activeTrip.first())
        assertFalse(dataStore.data.first().contains(stringPreferencesKey("trip_location_id")))
    }

    @Test fun `legacyGtfsTripId treats a copy of the route id as no trip id`() {
        assertEquals("", legacyGtfsTripId(legacy = "5", routeId = "5"))
        assertEquals("", legacyGtfsTripId(legacy = null, routeId = "5"))
        assertEquals("trip-0830", legacyGtfsTripId(legacy = "trip-0830", routeId = "5"))
    }
}
