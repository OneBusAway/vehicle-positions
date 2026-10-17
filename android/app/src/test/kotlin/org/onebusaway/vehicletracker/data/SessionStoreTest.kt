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
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException
import java.security.GeneralSecurityException
import java.security.ProviderException
import java.util.Base64

/**
 * Exercises [EncryptedSessionStore] against a real file-backed DataStore and a [FakeCryptor],
 * covering the upgrade from the build that stored the driver's JWT in the clear. These run on the
 * JVM; `KeystoreCryptorTest` covers the parts that need a device.
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

        store.saveLogin(SERVER, EMAIL, TOKENS)

        assertEquals(SIGNED_IN, store.session.first())
    }

    @Test fun `neither token reaches the file in the clear`() = runTest {
        val file = File(tempFolder.newFolder(), "session.preferences_pb")
        val dataStore = newDataStore(file)

        EncryptedSessionStore(dataStore, FakeCryptor()).saveLogin(SERVER, EMAIL, TOKENS)

        val prefs = dataStore.data.first()
        assertNull("the plaintext key must not survive a save", prefs[LEGACY_TOKEN_KEY])
        assertNotEquals(TOKEN, prefs[TOKEN_KEY])
        assertEquals(FakeCryptor().encrypt(REFRESH_TOKEN), prefs[REFRESH_TOKEN_KEY])
        val onDisk = file.readText(Charsets.ISO_8859_1)
        assertFalse("the token appears verbatim in $file", onDisk.contains(TOKEN))
        assertFalse("the refresh token appears verbatim in $file", onDisk.contains(REFRESH_TOKEN))
    }

    @Test fun `a refreshed pair replaces the old one and keeps the server url and the email`() = runTest {
        val store = EncryptedSessionStore(newDataStore(), FakeCryptor())
        store.saveLogin(SERVER, EMAIL, TOKENS)

        store.saveRefreshed(IssuedTokens("a2", "r2", ISSUED_AT + 1000, ISSUED_AT + 100))

        assertEquals(
            SIGNED_IN.copy(token = "a2", issuedAtEpochSec = ISSUED_AT + 100, refreshToken = "r2", accessExpiresAtEpochSec = ISSUED_AT + 1000),
            store.session.first(),
        )
    }

    @Test fun `dropping the refresh token keeps the access token, its expiry and the email`() = runTest {
        val store = EncryptedSessionStore(newDataStore(), FakeCryptor())
        store.saveLogin(SERVER, EMAIL, TOKENS)

        store.dropRefreshToken()

        assertEquals(SIGNED_IN.copy(refreshToken = null), store.session.first())
    }

    @Test fun `clearing the token removes both tokens and their times, and keeps the server url and the email`() = runTest {
        val dataStore = newDataStore()
        val store = EncryptedSessionStore(dataStore, FakeCryptor())
        store.saveLogin(SERVER, EMAIL, TOKENS)

        store.clearToken()

        assertEquals(SIGNED_OUT, store.session.first())
        assertEquals(setOf("server_url", "email"), dataStore.data.first().asMap().keys.map { it.name }.toSet())
    }

    @Test fun `a session saved before the email was kept still reads, with no email`() = runTest {
        val dataStore = newDataStore()
        // The keys exactly as the build before this one wrote them.
        dataStore.edit { prefs ->
            prefs[stringPreferencesKey("server_url")] = SERVER
            prefs[TOKEN_KEY] = FakeCryptor().encrypt(TOKEN)
            prefs[longPreferencesKey("token_issued_at")] = ISSUED_AT
            prefs[REFRESH_TOKEN_KEY] = FakeCryptor().encrypt(REFRESH_TOKEN)
            prefs[longPreferencesKey("token_expires_at")] = EXPIRES_AT
        }

        assertEquals(SIGNED_IN.copy(email = null), EncryptedSessionStore(dataStore, FakeCryptor()).session.first())
    }

    @Test fun `the session flow re-emits after each write`() = runTest {
        val store = EncryptedSessionStore(newDataStore(), FakeCryptor())

        store.session.test {
            assertEquals(Session(null, null, null), awaitItem())
            store.saveLogin(SERVER, EMAIL, TOKENS)
            assertEquals(SIGNED_IN, awaitItem())
            store.clearToken()
            assertEquals(SIGNED_OUT, awaitItem())
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
        EncryptedSessionStore(dataStore, cryptor).saveLogin(SERVER, EMAIL, TOKENS)

        cryptor.failDecrypt = true
        val session = EncryptedSessionStore(dataStore, cryptor).session.first()

        // A null token is what sends the driver back to login.
        assertNull(session.token)
        assertFalse(session.isSignedIn(ISSUED_AT))
        // Nothing is erased on a failure that may be transient.
        assertEquals(SERVER, session.serverUrl)
        assertTrue(dataStore.data.first().contains(TOKEN_KEY))
    }

    @Test fun `an undecryptable refresh token reads as none and leaves the access token readable`() = runTest {
        val dataStore = newDataStore()
        EncryptedSessionStore(dataStore, FakeCryptor()).saveLogin(SERVER, EMAIL, TOKENS)
        dataStore.edit { prefs -> prefs[REFRESH_TOKEN_KEY] = "written under a key that is gone" }

        val session = EncryptedSessionStore(dataStore, FakeCryptor()).session.first()

        assertNull(session.refreshToken)
        assertEquals(TOKEN, session.token)
        assertEquals(EXPIRES_AT, session.accessExpiresAtEpochSec)
    }

    @Test fun `a session saved by the build before refresh reads back with no refresh token and no expiry`() = runTest {
        val dataStore = newDataStore()
        dataStore.edit { prefs ->
            prefs[stringPreferencesKey("server_url")] = SERVER
            prefs[TOKEN_KEY] = FakeCryptor().encrypt(TOKEN)
            prefs[longPreferencesKey("token_issued_at")] = ISSUED_AT
        }

        val session = EncryptedSessionStore(dataStore, FakeCryptor()).session.first()

        assertEquals(Session(SERVER, TOKEN, ISSUED_AT, refreshToken = null, accessExpiresAtEpochSec = null), session)
    }

    @Test fun `saveLogin writes nothing when encryption fails`() = runTest {
        val dataStore = newDataStore()
        val store = EncryptedSessionStore(dataStore, FakeCryptor().apply { failEncrypt = true })

        val failure = runCatching { store.saveLogin(SERVER, EMAIL, TOKENS) }.exceptionOrNull()

        assertTrue("expected a GeneralSecurityException, got $failure", failure is GeneralSecurityException)
        assertTrue("a half-written session is worse than none", dataStore.data.first().asMap().isEmpty())
    }

    @Test fun `an unchecked cryptor failure is not mistaken for a logged-out driver`() = runTest {
        val dataStore = newDataStore()
        seedLegacySession(dataStore)
        // The store's catches are narrow on purpose, so a Cryptor that breaks its contract
        // surfaces instead of being quietly read as "no token". Keeping real Keystore failures
        // inside that contract is KeystoreCryptor's job — it wraps every Keystore call, and
        // `normalizingKeystoreFailures` below covers the conversion itself.
        val cryptor = FakeCryptor().apply {
            failEncrypt = true
            encryptFailure = ProviderException("Failed to communicate with keystore service")
        }

        val failure = runCatching {
            EncryptedSessionStore(dataStore, cryptor).session.first()
        }.exceptionOrNull()

        assertTrue("expected the ProviderException to surface, got $failure", failure is ProviderException)
    }

    @Test fun `an unchecked keystore failure is reported as a GeneralSecurityException`() {
        val failure = runCatching {
            normalizingKeystoreFailures("boom") { throw ProviderException("Keystore key generation failed") }
        }.exceptionOrNull()

        assertTrue("expected a GeneralSecurityException, got $failure", failure is GeneralSecurityException)
        assertTrue("the cause must survive for the log", failure?.cause is ProviderException)
    }

    @Test fun `normalizing leaves other failures alone so the retry still sees them`() {
        val original = GeneralSecurityException("unreadable")

        val failure = runCatching { normalizingKeystoreFailures("boom") { throw original } }.exceptionOrNull()

        assertSame(original, failure)
    }

    @Test fun `an unreadable session file reads as logged out`() = runTest {
        val store = EncryptedSessionStore(FailingDataStore(IOException("session file is gone")), FakeCryptor())

        assertEquals(Session(null, null, null), store.session.first())
    }

    @Test fun `a non-IO storage failure is not swallowed`() = runTest {
        val store = EncryptedSessionStore(FailingDataStore(IllegalStateException("a bug, not a bad disk")), FakeCryptor())

        val failure = runCatching { store.session.first() }.exceptionOrNull()

        assertTrue("expected an IllegalStateException, got $failure", failure is IllegalStateException)
    }

    @Test fun `a refresh token keeps the driver signed in after the access token expires`() {
        val session = Session(SERVER, TOKEN, ISSUED_AT, REFRESH_TOKEN, EXPIRES_AT)

        assertTrue(session.isSignedIn(EXPIRES_AT + 3600))
    }

    @Test fun `with no refresh token, the driver is signed in until the access token expires`() {
        val session = Session(SERVER, TOKEN, ISSUED_AT, refreshToken = null, accessExpiresAtEpochSec = EXPIRES_AT)

        assertTrue(session.isSignedIn(EXPIRES_AT - 1))
        assertFalse(session.isSignedIn(EXPIRES_AT))
    }

    @Test fun `a session from the build before refresh lasts 24h from sign-in`() {
        val session = Session(SERVER, TOKEN, ISSUED_AT)

        assertTrue(session.isSignedIn(ISSUED_AT + 24 * 3600 - 1))
        assertFalse(session.isSignedIn(ISSUED_AT + 24 * 3600))
    }

    @Test fun `no access token is never signed in, even with a refresh token`() {
        assertFalse(Session(SERVER, null, ISSUED_AT, REFRESH_TOKEN, EXPIRES_AT).isSignedIn(ISSUED_AT))
    }

    @Test fun `a token is refreshed a minute before it expires, or halfway through a shorter life`() {
        fun dueAfterIssue(lifetimeSec: Long) =
            Session(SERVER, TOKEN, ISSUED_AT, REFRESH_TOKEN, ISSUED_AT + lifetimeSec).refreshDueAtEpochSec!! - ISSUED_AT

        assertEquals(24 * 3600L - 60, dueAfterIssue(24 * 3600L))
        assertEquals(60L, dueAfterIssue(120L))
        assertEquals(30L, dueAfterIssue(60L))
    }

    @Test fun `a token with nothing to refresh it with, or no known expiry, is never due a refresh`() {
        assertNull(Session(SERVER, TOKEN, ISSUED_AT, refreshToken = null, accessExpiresAtEpochSec = EXPIRES_AT).refreshDueAtEpochSec)
        assertNull(Session(SERVER, TOKEN, ISSUED_AT).refreshDueAtEpochSec)
    }

    /** Stands in for storage that cannot be read at all, so the failure paths are reachable. */
    private class FailingDataStore(private val failure: Throwable) : DataStore<Preferences> {
        override val data: Flow<Preferences> = flow { throw failure }
        override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
            throw failure
    }

    private companion object {
        const val SERVER = "https://transit.example.org/"
        const val EMAIL = "maria@metro.example"
        const val TOKEN = "eyJhbGciOiJIUzI1NiJ9.driver42.signature"
        const val ISSUED_AT = 1_785_886_200L
        const val REFRESH_TOKEN = "3f9a17c0d2e84b65a1f0c9e7b3d24a68"
        const val EXPIRES_AT = ISSUED_AT + 24 * 3600
        val TOKENS = IssuedTokens(TOKEN, REFRESH_TOKEN, EXPIRES_AT, ISSUED_AT)

        // Named, as the email is a String? like the fields before it, so a field out of place would still compile.
        val SIGNED_IN = Session(
            serverUrl = SERVER,
            token = TOKEN,
            issuedAtEpochSec = ISSUED_AT,
            refreshToken = REFRESH_TOKEN,
            accessExpiresAtEpochSec = EXPIRES_AT,
            email = EMAIL,
        )
        val SIGNED_OUT = Session(serverUrl = SERVER, token = null, issuedAtEpochSec = null, email = EMAIL)

        val TOKEN_KEY = stringPreferencesKey("token_enc")
        val REFRESH_TOKEN_KEY = stringPreferencesKey("refresh_token_enc")
        val LEGACY_TOKEN_KEY = stringPreferencesKey("token")
    }

    @Test fun `the driver id is the sub of the server's token`() {
        assertEquals("2", Session("http://localhost/", jwtFor("2"), 0L).driverId)
        assertNull(Session("http://localhost/", null, null).driverId)
    }

    @Test fun `a token that is not a readable JWT has no driver id`() {
        val noSub = "e30." + Base64.getUrlEncoder().withoutPadding().encodeToString("""{"exp":1}""".toByteArray()) + ".sig"
        assertNull(jwtSubject("jwt"))
        assertNull(jwtSubject("e30.!!!.sig"))
        assertNull(jwtSubject("e30.WzFd.sig")) // [1]: JSON, but not an object
        assertNull(jwtSubject(noSub))
    }
}
