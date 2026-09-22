package org.onebusaway.vehicletracker.data

import app.cash.turbine.test
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
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.security.GeneralSecurityException

/**
 * Exercises [EncryptedSessionStore] against a real file-backed DataStore and a [FakeCryptor],
 * covering the upgrade from the build that stored the driver's JWT in the clear. These run on the
 * JVM, which is the only thing CI executes — see `KeystoreCryptorTest` for the parts that need a
 * device.
 */
class SessionStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val scopes = mutableListOf<CoroutineScope>()

    @After fun tearDown() = scopes.forEach { it.cancel() }

    /** DataStore allows one live instance per file, so every test gets its own file and scope. */
    private fun newDataStore(
        file: File = File(tempFolder.newFolder(), "session.preferences_pb"),
    ): DataStore<Preferences> = PreferenceDataStoreFactory.create(
        scope = CoroutineScope(Job() + Dispatchers.IO).also { scopes += it },
        produceFile = { file },
    )

    /** Writes the keys exactly as the build before the encrypted store did. */
    private suspend fun seedLegacySession(dataStore: DataStore<Preferences>, token: String = TOKEN) {
        dataStore.edit { prefs ->
            prefs[stringPreferencesKey("server_url")] = SERVER
            prefs[stringPreferencesKey("token")] = token
            prefs[longPreferencesKey("token_issued_at")] = ISSUED_AT
        }
    }

    @Test fun `a saved login reads back unchanged`() = runTest {
        val store = EncryptedSessionStore(newDataStore(), FakeCryptor())

        store.saveLogin(SERVER, TOKEN, ISSUED_AT)

        assertEquals(Session(SERVER, TOKEN, ISSUED_AT), store.session.first())
    }

    @Test fun `the token never reaches the file in the clear`() = runTest {
        val file = File(tempFolder.newFolder(), "session.preferences_pb")
        val dataStore = newDataStore(file)

        EncryptedSessionStore(dataStore, FakeCryptor()).saveLogin(SERVER, TOKEN, ISSUED_AT)

        val prefs = dataStore.data.first()
        assertNull("the plaintext key must not survive a save", prefs[LEGACY_TOKEN_KEY])
        assertNotEquals(TOKEN, prefs[TOKEN_KEY])
        assertFalse(
            "the token appears verbatim in $file",
            file.readText(Charsets.ISO_8859_1).contains(TOKEN),
        )
    }

    @Test fun `clearing the token keeps the server url`() = runTest {
        val store = EncryptedSessionStore(newDataStore(), FakeCryptor())
        store.saveLogin(SERVER, TOKEN, ISSUED_AT)

        store.clearToken()

        assertEquals(Session(SERVER, null, null), store.session.first())
    }

    @Test fun `the session flow re-emits after each write`() = runTest {
        val store = EncryptedSessionStore(newDataStore(), FakeCryptor())

        store.session.test {
            assertEquals(Session(null, null, null), awaitItem())
            store.saveLogin(SERVER, TOKEN, ISSUED_AT)
            assertEquals(Session(SERVER, TOKEN, ISSUED_AT), awaitItem())
            store.clearToken()
            assertEquals(Session(SERVER, null, null), awaitItem())
            cancelAndIgnoreRemainingEvents()
        }
    }

    @Test fun `a legacy plaintext token is encrypted and stays readable`() = runTest {
        val dataStore = newDataStore()
        seedLegacySession(dataStore)

        val session = EncryptedSessionStore(dataStore, FakeCryptor()).session.first()

        assertEquals("the driver must not be signed out by the upgrade", TOKEN, session.token)
        val prefs = dataStore.data.first()
        assertNull("the plaintext key must be gone afterwards", prefs[LEGACY_TOKEN_KEY])
        assertEquals(FakeCryptor().encrypt(TOKEN), prefs[TOKEN_KEY])
    }

    @Test fun `migration does not overwrite a token that is already encrypted`() = runTest {
        val dataStore = newDataStore()
        val cryptor = FakeCryptor()
        dataStore.edit { prefs ->
            prefs[stringPreferencesKey("server_url")] = SERVER
            prefs[TOKEN_KEY] = cryptor.encrypt("newer-token")
            prefs[LEGACY_TOKEN_KEY] = "stale-token"
            prefs[longPreferencesKey("token_issued_at")] = ISSUED_AT
        }

        val session = EncryptedSessionStore(dataStore, cryptor).session.first()

        assertEquals("newer-token", session.token)
        assertNull(dataStore.data.first()[LEGACY_TOKEN_KEY])
    }

    @Test fun `migration is a no-op on a fresh install`() = runTest {
        val dataStore = newDataStore()

        val session = EncryptedSessionStore(dataStore, FakeCryptor()).session.first()

        assertEquals(Session(null, null, null), session)
        assertTrue("nothing should be written on a fresh install", dataStore.data.first().asMap().isEmpty())
    }

    @Test fun `a failed migration leaves the driver logged in`() = runTest {
        val dataStore = newDataStore()
        seedLegacySession(dataStore)

        val session = EncryptedSessionStore(dataStore, FakeCryptor().apply { failEncrypt = true }).session.first()

        // The driver keeps working on the plaintext token and the next launch retries.
        assertEquals(TOKEN, session.token)
        val prefs = dataStore.data.first()
        assertEquals("the legacy token must survive a failed migration", TOKEN, prefs[LEGACY_TOKEN_KEY])
        assertFalse("nothing partial should be committed", prefs.contains(TOKEN_KEY))
    }

    @Test fun `an undecryptable token reads as logged out`() = runTest {
        val dataStore = newDataStore()
        val cryptor = FakeCryptor()
        EncryptedSessionStore(dataStore, cryptor).saveLogin(SERVER, TOKEN, ISSUED_AT)

        cryptor.failDecrypt = true
        val session = EncryptedSessionStore(dataStore, cryptor).session.first()

        // A null token is what sends the driver back to login.
        assertNull(session.token)
        assertFalse(session.hasFreshToken(ISSUED_AT))
        // Nothing is erased on a failure that may be transient.
        assertEquals(SERVER, session.serverUrl)
        assertTrue(dataStore.data.first().contains(TOKEN_KEY))
    }

    @Test fun `saveLogin writes nothing when encryption fails`() = runTest {
        val dataStore = newDataStore()
        val store = EncryptedSessionStore(dataStore, FakeCryptor().apply { failEncrypt = true })

        val failure = runCatching { store.saveLogin(SERVER, TOKEN, ISSUED_AT) }.exceptionOrNull()

        assertTrue("expected a GeneralSecurityException, got $failure", failure is GeneralSecurityException)
        assertTrue("a half-written session is worse than none", dataStore.data.first().asMap().isEmpty())
    }

    @Test fun `hasFreshToken holds one second before 24h and lapses at 24h`() {
        val session = Session(SERVER, TOKEN, ISSUED_AT)

        assertTrue(session.hasFreshToken(ISSUED_AT + 24 * 3600 - 1))
        assertFalse(session.hasFreshToken(ISSUED_AT + 24 * 3600))
    }

    private companion object {
        const val SERVER = "https://transit.example.org/"
        const val TOKEN = "eyJhbGciOiJIUzI1NiJ9.driver42.signature"
        const val ISSUED_AT = 1_785_886_200L

        val TOKEN_KEY = stringPreferencesKey("token_enc")
        val LEGACY_TOKEN_KEY = stringPreferencesKey("token")
    }
}
