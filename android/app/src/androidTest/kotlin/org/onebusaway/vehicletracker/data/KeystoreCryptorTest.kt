package org.onebusaway.vehicletracker.data

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/**
 * Covers the one piece [SessionStoreTest] cannot reach on the JVM: the real Android Keystore
 * binding. CI runs no instrumented tests (`.github/workflows/android.yml` runs `assembleDebug`,
 * `testDebugUnitTest`, `assembleRelease` and starts no emulator), so run these with
 * `./gradlew :app:connectedDebugAndroidTest` on a device or emulator.
 */
@RunWith(AndroidJUnit4::class)
class KeystoreCryptorTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val file = File(context.filesDir, "keystore_cryptor_test.preferences_pb")
    private val scope = CoroutineScope(Job() + Dispatchers.IO)

    @After fun tearDown() {
        scope.cancel()
        file.delete()
    }

    @Test
    fun aTokenRoundTripsThroughTheKeystore() {
        val cryptor = KeystoreCryptor()

        assertEquals(TOKEN, cryptor.decrypt(cryptor.encrypt(TOKEN)))
    }

    @Test
    fun theSameTokenEncryptsDifferentlyEachTime() {
        val cryptor = KeystoreCryptor()

        // GCM must never reuse a nonce under the same key, so two encryptions of one value differ.
        assertNotEquals(cryptor.encrypt(TOKEN), cryptor.encrypt(TOKEN))
    }

    @Test
    fun theCiphertextDoesNotContainTheToken() {
        assertFalse(KeystoreCryptor().encrypt(TOKEN).contains(TOKEN))
    }

    @Test
    fun aTokenSurvivesANewCryptorOverTheSameKey() {
        val ciphertext = KeystoreCryptor().encrypt(TOKEN)

        // A fresh instance stands in for the next process: it reloads the key by alias.
        assertEquals(TOKEN, KeystoreCryptor().decrypt(ciphertext))
    }

    /** The test the whole change exists for: the token must not be legible on disk. */
    @Test
    fun theSessionFileHoldsNoPlaintextToken() = runBlocking {
        val dataStore = PreferenceDataStoreFactory.create(scope = scope, produceFile = { file })
        val store = EncryptedSessionStore(dataStore, KeystoreCryptor())

        store.saveLogin(SERVER, TOKEN, ISSUED_AT)

        assertFalse(
            "the token appears verbatim in $file",
            file.readText(Charsets.ISO_8859_1).contains(TOKEN),
        )
    }

    private companion object {
        const val SERVER = "https://transit.example.org/"
        const val TOKEN = "eyJhbGciOiJIUzI1NiJ9.driver42.signature"
        const val ISSUED_AT = 1_785_886_200L
    }
}
