package org.onebusaway.vehicletracker.data

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException

/** [DataStoreTripSettingsStore] against a real file-backed DataStore, as [VehiclePrefsStoreTest]. */
class TripSettingsStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val scope = CoroutineScope(Job() + Dispatchers.IO)

    @After fun tearDown() = scope.cancel()

    private fun dataStore() = File(tempFolder.newFolder(), "trip_settings.preferences_pb").let { file ->
        PreferenceDataStoreFactory.create(scope = scope, produceFile = { file })
    }

    @Test fun `with nothing chosen, Tracking does what it always did`() = runTest {
        assertEquals(TripSettings(ScreenOn.CHARGING, MapUp.HEADING, vibrates = true), DataStoreTripSettingsStore(dataStore()).settings.first())
    }

    @Test fun `each choice is kept, and changes nothing else`() = runTest {
        val store = DataStoreTripSettingsStore(dataStore())

        store.setScreenOn(ScreenOn.ALWAYS)
        assertEquals(TripSettings(screenOn = ScreenOn.ALWAYS), store.settings.first())
        store.setMapUp(MapUp.NORTH)
        assertEquals(TripSettings(ScreenOn.ALWAYS, MapUp.NORTH), store.settings.first())
        store.setVibrates(false)
        assertEquals(TripSettings(ScreenOn.ALWAYS, MapUp.NORTH, vibrates = false), store.settings.first())
    }

    @Test fun `each can be put back`() = runTest {
        val store = DataStoreTripSettingsStore(dataStore())
        store.setScreenOn(ScreenOn.ALWAYS)
        store.setMapUp(MapUp.NORTH)
        store.setVibrates(false)

        store.setScreenOn(ScreenOn.CHARGING)
        store.setMapUp(MapUp.HEADING)
        store.setVibrates(true)

        assertEquals(TripSettings(), store.settings.first())
    }

    @Test fun `a name this version does not know is the default`() = runTest {
        val dataStore = dataStore()
        dataStore.edit {
            it[stringPreferencesKey("screen_on")] = "sometimes"
            it[stringPreferencesKey("map_up")] = "south"
        }

        assertEquals(TripSettings(), DataStoreTripSettingsStore(dataStore).settings.first())
    }

    @Test fun `choices that can't be read are the defaults`() = runTest {
        val store = DataStoreTripSettingsStore(FailingDataStore(IOException("the file is gone")))

        assertEquals(TripSettings(), store.settings.first())
    }

    // CodeRabbit on #666: one failed read used to leave the defaults for as long as the screen was up.
    @Test fun `after a read failure the defaults show, and the choices come back once they can be read`() = runTest {
        val disk = MemoryDataStore()
        DataStoreTripSettingsStore(disk).setMapUp(MapUp.NORTH)
        val store = DataStoreTripSettingsStore(FailingAtFirst(disk, failures = 2))
        val seen = mutableListOf<TripSettings>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { store.settings.toList(seen) }

        assertEquals(listOf(TripSettings()), seen)
        advanceTimeBy(READ_RETRY_DELAY)
        runCurrent()
        assertEquals("still failing, and the defaults not again", listOf(TripSettings()), seen)
        advanceTimeBy(READ_RETRY_DELAY)
        runCurrent()
        assertEquals(listOf(TripSettings(), TripSettings(mapUp = MapUp.NORTH)), seen)

        store.setVibrates(false)
        runCurrent()
        assertEquals("and a later choice shows", TripSettings(mapUp = MapUp.NORTH, vibrates = false), seen.last())
    }

    @Test fun `a non-IO storage failure is not swallowed`() = runTest {
        val store = DataStoreTripSettingsStore(FailingDataStore(IllegalStateException("a bug, not a bad disk")))

        val failure = runCatching { store.settings.first() }.exceptionOrNull()

        assertTrue("expected an IllegalStateException, got $failure", failure is IllegalStateException)
    }
}

/** Preferences in memory, as a DataStore holds them. */
private class MemoryDataStore : DataStore<Preferences> {
    private val stored = MutableStateFlow(emptyPreferences())
    override val data: Flow<Preferences> = stored
    override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
        transform(stored.value).also { stored.value = it }
}

/** [disk], whose first [failures] reads fail as a busy or damaged file does. */
private class FailingAtFirst(private val disk: DataStore<Preferences>, private var failures: Int) : DataStore<Preferences> {
    override val data: Flow<Preferences> = flow {
        if (failures > 0) {
            failures--
            throw IOException("the file can't be read right now")
        }
        emitAll(disk.data)
    }
    override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences = disk.updateData(transform)
}
