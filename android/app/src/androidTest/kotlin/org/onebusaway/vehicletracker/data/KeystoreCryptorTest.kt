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
 * binding. CI runs these on an emulator in the `instrumented` job; locally, run
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

    /** The test the whole change exists for: the tokens must not be legible on disk. */
    @Test
    fun theSessionFileHoldsNoPlaintextToken() = runBlocking {
        val dataStore = PreferenceDataStoreFactory.create(scope = scope, produceFile = { file })
        val store = EncryptedSessionStore(dataStore, KeystoreCryptor())

        store.saveLogin(SERVER, "driver@transit.example.org", IssuedTokens(TOKEN, REFRESH_TOKEN, ISSUED_AT + 900, ISSUED_AT))

        val onDisk = file.readText(Charsets.ISO_8859_1)
        assertFalse("the token appears verbatim in $file", onDisk.contains(TOKEN))
        assertFalse("the refresh token appears verbatim in $file", onDisk.contains(REFRESH_TOKEN))
    }

    private companion object {
        const val SERVER = "https://transit.example.org/"
        const val TOKEN = "eyJhbGciOiJIUzI1NiJ9.driver42.signature"
        const val REFRESH_TOKEN = "3f9a17c0d2e84b65a1f0c9e7b3d24a68"
        const val ISSUED_AT = 1_785_886_200L
    }
}
