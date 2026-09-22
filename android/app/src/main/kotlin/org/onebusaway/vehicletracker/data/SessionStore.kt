package org.onebusaway.vehicletracker.data

import android.content.Context
import android.util.Log
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.map
import java.io.IOException
import java.security.GeneralSecurityException

data class Session(val serverUrl: String?, val token: String?, val issuedAtEpochSec: Long?) {
    fun hasFreshToken(nowEpochSec: Long): Boolean =
        token != null && issuedAtEpochSec != null && nowEpochSec - issuedAtEpochSec < 24 * 3600
}

interface SessionStore {
    val session: Flow<Session>
    suspend fun saveLogin(serverUrl: String, token: String, issuedAtEpochSec: Long)
    suspend fun clearToken()
}

/** Internal, not private, only because [org.onebusaway.vehicletracker.di.AppModule] provides the store. */
internal val Context.sessionDataStore: DataStore<Preferences> by preferencesDataStore(name = "session")

private const val LOG_TAG = "SessionStore"

/**
 * Persists the driver's session, with the JWT encrypted by [cryptor] before it reaches disk.
 *
 * Takes a `DataStore<Preferences>` rather than a `Context` — the same trade
 * [DataStoreTripStateStore] makes — so the upgrade path from the previous build can be
 * unit-tested without an emulator.
 *
 * Only the token is encrypted. The server URL and the issued-at timestamp are not credentials,
 * and leaving them in the clear keeps the upgrade to a single key.
 */
class EncryptedSessionStore(
    private val dataStore: DataStore<Preferences>,
    private val cryptor: Cryptor,
) : SessionStore {
    private object Keys {
        val SERVER_URL = stringPreferencesKey("server_url")
        val TOKEN = stringPreferencesKey("token_enc")
        val TOKEN_ISSUED_AT = longPreferencesKey("token_issued_at")

        /**
         * The plaintext token written by builds before this one, read once so an upgrade does not
         * sign out every logged-in driver, then removed.
         *
         * TODO: drop this key and [migrateLegacyToken] one release after the encrypted store ships.
         */
        val LEGACY_TOKEN = stringPreferencesKey("token")
    }

    override val session: Flow<Session> = flow {
        migrateLegacyToken()
        emitAll(
            dataStore.data.map { prefs ->
                Session(
                    serverUrl = prefs[Keys.SERVER_URL],
                    token = readToken(prefs),
                    issuedAtEpochSec = prefs[Keys.TOKEN_ISSUED_AT],
                )
            },
        )
    }

    /**
     * Re-encrypts a token left by a build that stored it in the clear.
     *
     * The whole upgrade is one DataStore transaction: the ciphertext lands and the plaintext key
     * is dropped together, or neither happens. So an interruption — or a Keystore that cannot
     * encrypt right now — leaves the legacy token exactly where it was, [readToken] keeps serving
     * it, and the next launch retries. A failed migration never signs the driver out.
     */
    private suspend fun migrateLegacyToken() {
        try {
            if (!dataStore.data.first().contains(Keys.LEGACY_TOKEN)) return
            dataStore.edit { prefs ->
                val legacy = prefs[Keys.LEGACY_TOKEN] ?: return@edit
                // An already-encrypted token wins: it is at least as new as the plaintext one, so
                // re-encrypting would roll the session back.
                if (!prefs.contains(Keys.TOKEN)) {
                    prefs[Keys.TOKEN] = cryptor.encrypt(legacy)
                }
                prefs.remove(Keys.LEGACY_TOKEN)
            }
        } catch (e: GeneralSecurityException) {
            Log.w(LOG_TAG, "Could not encrypt the stored token; leaving it for the next launch.", e)
        } catch (e: IOException) {
            Log.w(LOG_TAG, "Could not read session storage to encrypt the stored token.", e)
        }
    }

    /**
     * A token that cannot be decrypted — the Keystore key is gone after a restore to a new device,
     * or the entry was invalidated — is reported as no token rather than deleted. The flow emits
     * `token = null`, the existing launch-destination check sends the driver to login, and nothing
     * is erased on what may be a transient failure.
     */
    private fun readToken(prefs: Preferences): String? {
        val stored = prefs[Keys.TOKEN] ?: return prefs[Keys.LEGACY_TOKEN]
        return try {
            cryptor.decrypt(stored)
        } catch (e: GeneralSecurityException) {
            Log.w(LOG_TAG, "Stored token could not be decrypted; treating the driver as logged out.", e)
            null
        }
    }

    override suspend fun saveLogin(serverUrl: String, token: String, issuedAtEpochSec: Long) {
        // Encrypt before opening the transaction so a Keystore failure reaches the caller —
        // AuthRepository.login turns it into Result.failure — without half-writing a session.
        val encrypted = cryptor.encrypt(token)
        dataStore.edit { prefs ->
            prefs[Keys.SERVER_URL] = serverUrl
            prefs[Keys.TOKEN] = encrypted
            prefs[Keys.TOKEN_ISSUED_AT] = issuedAtEpochSec
            prefs.remove(Keys.LEGACY_TOKEN)
        }
    }

    override suspend fun clearToken() {
        dataStore.edit { prefs ->
            prefs.remove(Keys.TOKEN)
            prefs.remove(Keys.TOKEN_ISSUED_AT)
            prefs.remove(Keys.LEGACY_TOKEN)
        }
    }
}
