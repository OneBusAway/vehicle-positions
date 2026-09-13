package org.onebusaway.vehicletracker.data

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.job
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * Exercises [DataStoreVehiclePrefsStore] against a real file-backed DataStore. That is why the
 * store takes a `DataStore<Preferences>` rather than a `Context` — Android CI runs
 * `testDebugUnitTest` with no emulator, so a Context-bound store could not be covered here.
 */
class VehiclePrefsStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val scopes = mutableListOf<CoroutineScope>()

    @After fun tearDown() = scopes.forEach { it.cancel() }

    /** DataStore allows one live instance per file, so every store gets its own file and scope. */
    private fun newStore(file: File = File(tempFolder.newFolder(), "vehicle_prefs.preferences_pb")) =
        DataStoreVehiclePrefsStore(
            PreferenceDataStoreFactory.create(
                scope = CoroutineScope(Job() + Dispatchers.IO).also { scopes += it },
                produceFile = { file },
            ),
        )

    @Test fun `a fresh store has no favorites or recents`() = runTest {
        val store = newStore()

        assertEquals(emptySet<String>(), store.favorites.first())
        assertEquals(emptyList<String>(), store.recents.first())
    }

    @Test fun `toggling a favorite adds it and toggling again removes it`() = runTest {
        val store = newStore()

        store.toggleFavorite("bus-1")
        store.toggleFavorite("van-2")
        assertEquals(setOf("bus-1", "van-2"), store.favorites.first())

        store.toggleFavorite("bus-1")
        assertEquals(setOf("van-2"), store.favorites.first())
    }

    @Test fun `recents keep five and the sixth evicts the oldest`() = runTest {
        val store = newStore()

        for (id in listOf("bus-1", "bus-2", "bus-3", "bus-4", "bus-5")) store.recordUse(id)
        assertEquals(listOf("bus-5", "bus-4", "bus-3", "bus-2", "bus-1"), store.recents.first())

        store.recordUse("bus-6")
        assertEquals(listOf("bus-6", "bus-5", "bus-4", "bus-3", "bus-2"), store.recents.first())
    }

    @Test fun `recording a known vehicle moves it to the front without duplicating`() = runTest {
        val store = newStore()

        for (id in listOf("bus-1", "bus-2", "bus-3")) store.recordUse(id)
        store.recordUse("bus-1")

        assertEquals(listOf("bus-1", "bus-3", "bus-2"), store.recents.first())
    }

    @Test fun `the list delimiter is stripped from stored ids`() = runTest {
        val store = newStore()

        store.recordUse("bus|1")
        store.toggleFavorite("van|2")

        // Left in, the pipe would decode back as two entries rather than one id.
        assertEquals(listOf("bus1"), store.recents.first())
        assertEquals(setOf("van2"), store.favorites.first())
    }

    @Test fun `favorites and recents survive a new store over the same file`() = runTest {
        val file = File(tempFolder.newFolder(), "vehicle_prefs.preferences_pb")
        val scope = CoroutineScope(Job() + Dispatchers.IO)
        val first = DataStoreVehiclePrefsStore(
            PreferenceDataStoreFactory.create(scope = scope, produceFile = { file }),
        )
        first.toggleFavorite("bus-1")
        first.recordUse("van-2")

        // Releasing the first instance is what lets a second one read the bytes back off disk,
        // the way the next app launch does.
        scope.cancel()
        scope.coroutineContext.job.join()

        val second = newStore(file)
        assertEquals(setOf("bus-1"), second.favorites.first())
        assertEquals(listOf("van-2"), second.recents.first())
    }
}
