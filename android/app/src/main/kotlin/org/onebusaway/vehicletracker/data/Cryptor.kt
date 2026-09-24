package org.onebusaway.vehicletracker.data

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.util.Log
import java.io.IOException
import java.security.GeneralSecurityException
import java.security.KeyStore
import java.security.ProviderException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Encrypts the driver's JWT before it reaches disk.
 *
 * This is a seam rather than an abstraction for its own sake: [EncryptedSessionStore] holds all
 * of the session, migration and recovery logic and is exercised on the JVM against a fake, while
 * [KeystoreCryptor] — the only part that needs a real device — is covered by instrumented tests.
 * CI runs `testDebugUnitTest` and no emulator, so the logic that has to gate a change sits on
 * this side of the seam.
 */
interface Cryptor {
    /** @throws GeneralSecurityException if the key cannot be created or used. */
    fun encrypt(plaintext: String): String

    /** @throws GeneralSecurityException if the key is gone or the ciphertext does not verify. */
    fun decrypt(ciphertext: String): String
}

/**
 * Runs a Keystore call, converting the provider's unchecked [ProviderException] into the
 * [GeneralSecurityException] that [Cryptor] documents — the same normalisation [KeystoreCryptor]
 * applies to malformed Base64 in [KeystoreCryptor.decrypt].
 *
 * Some devices report a Keystore that cannot generate or read a key as `ProviderException`
 * ("Keystore key generation failed"), which is a `RuntimeException` and so slips past every
 * `GeneralSecurityException` catch between here and the UI. Unconverted it escapes the session
 * flow and cancels its collectors, and because a failed migration leaves the legacy token in
 * place, the next launch would hit it again — turning a device with a broken Keystore into a
 * crash on every launch, for a driver who was simply logged in before.
 */
internal fun <T> normalizingKeystoreFailures(message: String, block: () -> T): T = try {
    block()
} catch (e: ProviderException) {
    throw GeneralSecurityException(message, e)
}

private const val LOG_TAG = "KeystoreCryptor"
private const val KEYSTORE = "AndroidKeyStore"
private const val KEY_ALIAS = "session_token"
private const val TRANSFORMATION = "AES/GCM/NoPadding"
private const val NONCE_BYTES = 12
private const val GCM_TAG_BITS = 128

/**
 * AES-256-GCM using a key held in the Android Keystore, where the app can use it but never read
 * it, and which no backup or device transfer carries off the device.
 *
 * Deliberately not `androidx.security:security-crypto`. Google deprecated every API in that
 * library, `EncryptedSharedPreferences` included, "in favour of existing platform APIs and direct
 * use of Android Keystore" (1.1.0-alpha07, April 2025, carried into 1.1.0 stable). This is that
 * replacement, and it adds no dependency.
 *
 * The key is intentionally not bound to user authentication: the tracking service posts locations
 * while the phone is locked, where an auth-bound key would be unusable, and such a key is
 * invalidated whenever the driver changes their screen lock.
 */
class KeystoreCryptor : Cryptor {
    @Volatile private var cached: SecretKey? = null

    override fun encrypt(plaintext: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ciphertext = cipher.doFinal(plaintext.toByteArray(Charsets.UTF_8))
        // GCM needs its nonce to decrypt and the nonce is not secret, so it travels with the
        // ciphertext. Cipher picks a fresh random one per call.
        return Base64.encodeToString(cipher.iv + ciphertext, Base64.NO_WRAP)
    }

    override fun decrypt(ciphertext: String): String {
        val raw = try {
            Base64.decode(ciphertext, Base64.NO_WRAP)
        } catch (e: IllegalArgumentException) {
            // Normalised so callers can catch GeneralSecurityException alone and still cover a
            // truncated or garbled stored value.
            throw GeneralSecurityException("stored token is not valid Base64", e)
        }
        if (raw.size <= NONCE_BYTES) {
            throw GeneralSecurityException("stored token is too short to hold a GCM nonce")
        }
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(GCM_TAG_BITS, raw, 0, NONCE_BYTES))
        return String(cipher.doFinal(raw, NONCE_BYTES, raw.size - NONCE_BYTES), Charsets.UTF_8)
    }

    /**
     * Returns the session key, regenerating it once if the Keystore entry cannot be read — a
     * documented failure on some devices, and the expected state after a restore to a new device.
     *
     * Wrapping the retry is what makes a second failure safe: a genuinely broken Keystore surfaces
     * as a [GeneralSecurityException] thrown from [encrypt] or [decrypt], which both declare and
     * [EncryptedSessionStore] handles. Resolving the key inside each call rather than from a
     * `by lazy` property keeps that failure at a defined point instead of escaping at whichever
     * call site happened to touch the key first.
     */
    private fun key(): SecretKey {
        cached?.let { return it }
        return synchronized(this) { cached ?: loadOrCreate().also { cached = it } }
    }

    private fun loadOrCreate(): SecretKey = try {
        existingKey() ?: generateKey()
    } catch (e: GeneralSecurityException) {
        Log.w(LOG_TAG, "Session key unreadable; regenerating it. Any stored token is now unreadable.", e)
        regenerate()
    } catch (e: IOException) {
        Log.w(LOG_TAG, "Keystore unreadable; regenerating the session key.", e)
        regenerate()
    }

    private fun regenerate(): SecretKey = try {
        deleteKey()
        generateKey()
    } catch (e: GeneralSecurityException) {
        throw GeneralSecurityException("session key could not be recreated", e)
    } catch (e: IOException) {
        throw GeneralSecurityException("session key could not be recreated", e)
    }

    private fun keyStore(): KeyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }

    private fun existingKey(): SecretKey? = normalizingKeystoreFailures("session key is unreadable") {
        (keyStore().getEntry(KEY_ALIAS, null) as? KeyStore.SecretKeyEntry)?.secretKey
    }

    private fun deleteKey() = normalizingKeystoreFailures("session key could not be deleted") {
        keyStore().deleteEntry(KEY_ALIAS)
    }

    private fun generateKey(): SecretKey = normalizingKeystoreFailures("session key could not be generated") {
        KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE).apply {
            init(
                KeyGenParameterSpec.Builder(
                    KEY_ALIAS,
                    KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
                )
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setKeySize(256)
                    .build(),
            )
        }.generateKey()
    }
}
