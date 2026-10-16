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
        val trip = ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L, driverId = "1")

        store.saveActiveTrip(trip)

        assertEquals(trip, store.activeTrip.first())
    }

    @Test fun `a trip saved before drivers were recorded reads back with no driver`() = runTest {
        val dataStore = newDataStore()
        // A trip in progress when the phone is updated: every key but trip_driver_id.
        dataStore.edit { prefs ->
            prefs[longPreferencesKey("trip_db_id")] = 7L
            prefs[stringPreferencesKey("trip_gtfs_trip_id")] = "trip-0830"
            prefs[stringPreferencesKey("trip_vehicle_id")] = "bus-1"
            prefs[stringPreferencesKey("trip_route_id")] = "5"
            prefs[stringPreferencesKey("trip_start_date")] = "20260804"
            prefs[longPreferencesKey("trip_started_at")] = 100L
        }

        val trip = DataStoreTripStateStore(dataStore, nairobi).activeTrip.first()

        assertEquals(ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L, driverId = null), trip)
    }

    @Test fun `a trip saved with no driver does not keep an earlier trip's driver`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)
        store.saveActiveTrip(ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L, driverId = "1"))

        store.saveActiveTrip(ActiveTrip(8L, "trip-0900", "bus-1", "5", "20260804", 200L, driverId = null))

        assertNull(store.activeTrip.first()!!.driverId)
    }

    @Test fun `clearing a trip removes its driver too`() = runTest {
        val dataStore = newDataStore()
        val store = DataStoreTripStateStore(dataStore, nairobi)
        store.saveActiveTrip(ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L, driverId = "1"))

        store.clearActiveTrip()

        assertFalse(dataStore.data.first().contains(stringPreferencesKey("trip_driver_id")))
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

    /** Starts trip [tripDbId] as [startedBy], then ends it on this phone only while [signedIn] is signed in. */
    private suspend fun TripStateStore.endLocally(tripDbId: Long, startedBy: String?, signedIn: String? = startedBy) {
        saveActiveTrip(ActiveTrip(tripDbId, "trip-0830", "bus-1", "5", "20260804", 100L, driverId = startedBy))
        endActiveTripLocally(signedInDriverId = signedIn)
    }

    @Test fun `a trip ended on this phone keeps its server id and driver until the end is confirmed`() = runTest {
        val dataStore = newDataStore()
        val store = DataStoreTripStateStore(dataStore, nairobi)

        store.endLocally(7L, startedBy = "2")

        assertNull(store.activeTrip.first())
        assertEquals(listOf(UnconfirmedEnd(7L, "2")), store.unconfirmedEnds.first())

        store.clearUnconfirmedEnd(7L)

        assertEquals(emptyList<UnconfirmedEnd>(), store.unconfirmedEnds.first())
        assertFalse(dataStore.data.first().contains(stringPreferencesKey("unconfirmed_ends")))
    }

    @Test fun `a second driver's end on this phone does not replace the first's`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)

        store.endLocally(7L, startedBy = "1")
        store.endLocally(8L, startedBy = "2")

        assertEquals(listOf(UnconfirmedEnd(7L, "1"), UnconfirmedEnd(8L, "2")), store.unconfirmedEnds.first())
    }

    @Test fun `a trip ended after another driver signs in is kept under the driver who started it`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)

        store.endLocally(7L, startedBy = "1", signedIn = "2")

        assertEquals(listOf(UnconfirmedEnd(7L, "1")), store.unconfirmedEnds.first())
    }

    @Test fun `a trip saved before drivers were recorded is kept under the driver signed in`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)

        store.endLocally(7L, startedBy = null, signedIn = "2")

        assertEquals(listOf(UnconfirmedEnd(7L, "2")), store.unconfirmedEnds.first())
    }

    @Test fun `a trip ended with no readable driver does not keep an earlier driver`() = runTest {
        val dataStore = newDataStore()
        val store = DataStoreTripStateStore(dataStore, nairobi)
        // Left behind by hand, as a write that never cleared it would.
        dataStore.edit { it[stringPreferencesKey("unconfirmed_end_driver_id")] = "1" }

        store.endLocally(7L, startedBy = null, signedIn = null)

        assertEquals(listOf(UnconfirmedEnd(7L, null)), store.unconfirmedEnds.first())
    }

    @Test fun `an end stored before drivers were recorded reads back without a driver`() = runTest {
        val dataStore = newDataStore()
        dataStore.edit { it[longPreferencesKey("unconfirmed_end_trip_db_id")] = 7L }

        assertEquals(listOf(UnconfirmedEnd(7L, null)), DataStoreTripStateStore(dataStore, nairobi).unconfirmedEnds.first())
    }

    @Test fun `an end left in the old single slot survives the next end on this phone`() = runTest {
        val dataStore = newDataStore()
        // As the build before ends were kept per driver wrote it.
        dataStore.edit { prefs ->
            prefs[longPreferencesKey("unconfirmed_end_trip_db_id")] = 7L
            prefs[stringPreferencesKey("unconfirmed_end_driver_id")] = "1"
        }
        val store = DataStoreTripStateStore(dataStore, nairobi)

        assertEquals(listOf(UnconfirmedEnd(7L, "1")), store.unconfirmedEnds.first())

        store.endLocally(8L, startedBy = "2")

        assertEquals(listOf(UnconfirmedEnd(7L, "1"), UnconfirmedEnd(8L, "2")), store.unconfirmedEnds.first())
        val prefs = dataStore.data.first()
        assertFalse(prefs.contains(longPreferencesKey("unconfirmed_end_trip_db_id")))
        assertFalse(prefs.contains(stringPreferencesKey("unconfirmed_end_driver_id")))
    }

    @Test fun `confirming an end left in the old single slot clears it`() = runTest {
        val dataStore = newDataStore()
        dataStore.edit { prefs ->
            prefs[longPreferencesKey("unconfirmed_end_trip_db_id")] = 7L
            prefs[stringPreferencesKey("unconfirmed_end_driver_id")] = "1"
        }
        val store = DataStoreTripStateStore(dataStore, nairobi)

        store.clearUnconfirmedEnd(7L)

        assertEquals(emptyList<UnconfirmedEnd>(), store.unconfirmedEnds.first())
    }

    @Test fun `confirming one end clears only that one`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)
        store.endLocally(7L, startedBy = "1")
        store.endLocally(8L, startedBy = "2")

        store.clearUnconfirmedEnd(7L)

        assertEquals(listOf(UnconfirmedEnd(8L, "2")), store.unconfirmedEnds.first())
    }

    @Test fun `ending the same trip twice keeps one end`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)

        store.endLocally(7L, startedBy = "1")
        store.endLocally(7L, startedBy = "1")

        assertEquals(listOf(UnconfirmedEnd(7L, "1")), store.unconfirmedEnds.first())
    }

    @Test fun `an unreadable list of ends reads as none instead of failing`() = runTest {
        val dataStore = newDataStore()
        dataStore.edit { it[stringPreferencesKey("unconfirmed_ends")] = "not json" }

        assertEquals(emptyList<UnconfirmedEnd>(), DataStoreTripStateStore(dataStore, nairobi).unconfirmedEnds.first())
    }

    @Test fun `ending locally with no stored trip leaves no end to send`() = runTest {
        val store = DataStoreTripStateStore(newDataStore(), nairobi)

        store.endActiveTripLocally(signedInDriverId = "2")

        assertEquals(emptyList<UnconfirmedEnd>(), store.unconfirmedEnds.first())
    }

    @Test fun `legacyGtfsTripId treats a copy of the route id as no trip id`() {
        assertEquals("", legacyGtfsTripId(legacy = "5", routeId = "5"))
        assertEquals("", legacyGtfsTripId(legacy = null, routeId = "5"))
        assertEquals("trip-0830", legacyGtfsTripId(legacy = "trip-0830", routeId = "5"))
    }
}
