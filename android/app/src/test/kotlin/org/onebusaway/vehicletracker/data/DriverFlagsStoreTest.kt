package org.onebusaway.vehicletracker.data

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.job
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException

/** What a driver has been shown, kept under a hash of who they are. Stands in for [DriverFlagsStore] in the view models' tests. */
class FakeDriverFlagsStore : DriverFlagsStore {
    val flags = mutableSetOf<Pair<DriverKey, DriverFlag>>()

    /** How many times a flag was set, whether or not it was new. */
    var sets = 0

    /** Thrown raw from both calls, as DataStore throws it. */
    var failure: IOException? = null

    override suspend fun isSet(driver: DriverKey, flag: DriverFlag): Boolean {
        failure?.let { throw it }
        return (driver to flag) in flags
    }

    override suspend fun set(driver: DriverKey, flag: DriverFlag) {
        failure?.let { throw it }
        sets++
        flags += driver to flag
    }
}

/** Exercises [DataStoreDriverFlagsStore] against a real file-backed DataStore, as [VehiclePrefsStoreTest] does its store. */
class DriverFlagsStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val scopes = mutableListOf<CoroutineScope>()

    @After fun tearDown() = scopes.forEach { it.cancel() }

    private fun newFile() = File(tempFolder.newFolder(), "driver_flags.preferences_pb")

    /**
     * DataStore allows one live instance per file, so a store that reopens a file first closes the one before and waits
     * for its scope to finish: only then is the file free, as in VehiclePrefsStoreTest.
     */
    private suspend fun storeOn(file: File): DataStoreDriverFlagsStore {
        scopes.forEach { it.coroutineContext.job.cancelAndJoin() }
        scopes.clear()
        return DataStoreDriverFlagsStore(
            PreferenceDataStoreFactory.create(
                scope = CoroutineScope(Job() + Dispatchers.IO).also { scopes += it },
                produceFile = { file },
            ),
        )
    }

    private val maria = DriverKey.of("https://tracker.example.org", "maria@metro.example")
    private val omar = DriverKey.of("https://tracker.example.org", "omar@metro.example")

    // --- Who a driver is ---

    @Test fun `a driver's key is the SHA-256 of the host and the email`() {
        // printf 'tracker.example.org|maria@metro.example' | shasum -a 256
        assertEquals("475edb74202a73b4664cf7aee5f3124923431382b443aff4616442b60bc477f0", maria.hash)
    }

    @Test fun `the key is the same however the server and the email were typed`() {
        assertEquals(maria, DriverKey.of("https://Tracker.Example.org/", " Maria@Metro.Example "))
        assertEquals("the path and the port are not part of who the driver is", maria, DriverKey.of("https://tracker.example.org:8443/api/", "maria@metro.example"))
    }

    @Test fun `another email or another server is another driver`() {
        assertNotEquals(maria, omar)
        assertNotEquals(maria, DriverKey.of("https://other.example.org", "maria@metro.example"))
    }

    @Test fun `a session names a driver only when it has both a server and an email`() {
        val session = Session("https://tracker.example.org/", token = "jwt", issuedAtEpochSec = 0L, email = "Maria@metro.example")

        assertEquals(maria, DriverKey.of(session))
        assertNull("one an older build stored has no email", DriverKey.of(session.copy(email = null)))
        assertNull(DriverKey.of(session.copy(serverUrl = null)))
    }

    // --- The store ---

    @Test fun `a fresh store has nothing set`() = runTest {
        assertFalse(storeOn(newFile()).isSet(maria, DriverFlag.WelcomeSeen))
    }

    @Test fun `a flag is set for its driver and for nobody else`() = runTest {
        val store = storeOn(newFile())

        store.set(maria, DriverFlag.WelcomeSeen)

        assertTrue(store.isSet(maria, DriverFlag.WelcomeSeen))
        assertFalse("the next driver on a shared phone has not seen it", store.isSet(omar, DriverFlag.WelcomeSeen))
    }

    @Test fun `a second driver's flag keeps the first one's`() = runTest {
        val store = storeOn(newFile())

        store.set(maria, DriverFlag.WelcomeSeen)
        store.set(omar, DriverFlag.WelcomeSeen)

        assertTrue(store.isSet(maria, DriverFlag.WelcomeSeen))
        assertTrue(store.isSet(omar, DriverFlag.WelcomeSeen))
    }

    @Test fun `a flag is still set when the app next opens`() = runTest {
        val file = newFile()
        storeOn(file).set(maria, DriverFlag.WelcomeSeen)

        assertTrue(storeOn(file).isSet(maria, DriverFlag.WelcomeSeen))
    }

    @Test fun `the email is not in what the phone keeps`() = runTest {
        val file = newFile()
        storeOn(file).set(maria, DriverFlag.WelcomeSeen)

        val kept = file.readBytes().toString(Charsets.ISO_8859_1)

        assertTrue("the hash is what is kept", maria.hash in kept)
        assertFalse(kept.contains("maria", ignoreCase = true))
        assertFalse(kept.contains("metro.example", ignoreCase = true))
    }

    @Test fun `a new flag is its own name and needs nothing else`() = runTest {
        val file = newFile()
        val runTip = DriverFlag("run_tip_seen")
        val store = storeOn(file)
        store.set(maria, DriverFlag.WelcomeSeen)

        assertFalse("a flag nobody has set reads unset, in a file written before it existed", store.isSet(maria, runTip))
        store.set(omar, runTip)

        val reopened = storeOn(file)
        assertTrue(reopened.isSet(omar, runTip))
        assertFalse(reopened.isSet(maria, runTip))
        assertTrue("and the flags before it are as they were", reopened.isSet(maria, DriverFlag.WelcomeSeen))
        assertFalse(reopened.isSet(omar, DriverFlag.WelcomeSeen))
    }

    @Test fun `each thing a driver is shown is its own record`() = runTest {
        val all = listOf(DriverFlag.WelcomeSeen, DriverFlag.RunBoardTipSeen, DriverFlag.SentCountTipSeen, DriverFlag.FirstTripDoneSeen)
        assertEquals("no two share a name", all.size, all.map { it.storedAs }.toSet().size)
        val store = storeOn(newFile())

        for (flag in all) {
            store.set(maria, flag)
            for (other in all) assertEquals("$other after $flag", all.indexOf(other) <= all.indexOf(flag), store.isSet(maria, other))
        }
    }

    @Test fun `a record that can't be read says so`() = runTest {
        val file = newFile().apply { writeText("not a preferences file") }
        val store = storeOn(file)

        assertThrows(IOException::class.java) { kotlinx.coroutines.runBlocking { store.isSet(maria, DriverFlag.WelcomeSeen) } }
    }
}
