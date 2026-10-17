package org.onebusaway.vehicletracker.data

import android.content.Context
import android.util.Log
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.map
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.onebusaway.vehicletracker.data.api.LoginResponse
import java.io.IOException
import java.security.GeneralSecurityException
import java.util.Base64

/**
 * The driver's session. [refreshToken] and [accessExpiresAtEpochSec] are null for a session saved
 * by a build that did not keep them.
 *
 * [email] is the one the driver last signed in with. It stays when the tokens are cleared, so
 * signing in again after a session ends asks only for the password; null before the first sign-in,
 * or when an older build stored the session.
 */
data class Session(
    val serverUrl: String?,
    val token: String?,
    val issuedAtEpochSec: Long?,
    val refreshToken: String? = null,
    val accessExpiresAtEpochSec: Long? = null,
    val email: String? = null,
) {
    /**
     * Whether requests can go out without the driver signing in again: there is an access token,
     * and either a refresh token to renew it with or time left on it. A refresh token the server
     * will refuse still counts until a request finds that out and drops it.
     */
    fun isSignedIn(nowEpochSec: Long): Boolean {
        if (token == null) return false
        if (refreshToken != null) return true
        val expiresAt = accessExpiresAtEpochSec
            ?: issuedAtEpochSec?.plus(OLD_BUILD_TOKEN_LIFETIME_SEC)
            ?: return false
        return nowEpochSec < expiresAt
    }

    /**
     * When to refresh the access token before it expires, so requests don't meet a 401: a minute
     * early, or halfway through a lifetime shorter than two minutes. Null when there is no refresh
     * token, or no expiry to count from.
     */
    val refreshDueAtEpochSec: Long?
        get() {
            if (refreshToken == null) return null
            val expiresAt = accessExpiresAtEpochSec ?: return null
            val issuedAt = issuedAtEpochSec ?: return null
            return expiresAt - minOf(MAX_EARLY_REFRESH_SEC, (expiresAt - issuedAt) / 2)
        }

    /** The signed-in driver's user id on the server, or null when there is no readable token. */
    val driverId: String? get() = token?.let(::jwtSubject)
}

/**
 * How long the builds before refresh took a token to last. A session they saved has no expiry and
 * no refresh token, so it is held to the same 24 hours from sign-in.
 */
private const val OLD_BUILD_TOKEN_LIFETIME_SEC = 24 * 3600L

private const val MAX_EARLY_REFRESH_SEC = 60L

/**
 * The `sub` claim of the server's JWT, which is the user id `/trips/end` checks a trip against.
 * Read without checking the signature: the token came from the server, and this only decides
 * which driver a trip stored on this phone belongs to.
 */
internal fun jwtSubject(token: String): String? = try {
    token.split('.').getOrNull(1)?.let { payload ->
        val claims = Json.parseToJsonElement(String(Base64.getUrlDecoder().decode(payload), Charsets.UTF_8))
        claims.jsonObject["sub"]?.jsonPrimitive?.contentOrNull
    }
} catch (e: IllegalArgumentException) {
    // Not Base64url, not JSON, or not a JSON object: no driver to read.
    null
}

/** What a sign-in or a refresh hands out, with its times on the phone's clock. */
data class IssuedTokens(
    val accessToken: String,
    val refreshToken: String,
    val accessExpiresAtEpochSec: Long,
    val issuedAtEpochSec: Long,
)

/**
 * The tokens in this reply, timed from [nowEpochSec], the moment it arrived. `expires_in` is
 * relative, so a phone clock that disagrees with the server's does not move the expiry.
 */
fun LoginResponse.issuedAt(nowEpochSec: Long) = IssuedTokens(
    accessToken = accessToken,
    refreshToken = refreshToken,
    accessExpiresAtEpochSec = nowEpochSec + expiresIn,
    issuedAtEpochSec = nowEpochSec,
)

interface SessionStore {
    val session: Flow<Session>
    suspend fun saveLogin(serverUrl: String, email: String, tokens: IssuedTokens)

    /** Saves the pair a refresh handed out, keeping the server URL and the email. */
    suspend fun saveRefreshed(tokens: IssuedTokens)

    /** Forgets the refresh token and keeps the access token, which works until it expires. */
    suspend fun dropRefreshToken()
    suspend fun clearToken()
}

/** Internal, not private, only because [org.onebusaway.vehicletracker.di.AppModule] provides the store. */
internal val Context.sessionDataStore: DataStore<Preferences> by preferencesDataStore(name = "session")

private const val LOG_TAG = "SessionStore"

/**
 * Persists the driver's session, with both tokens encrypted by [cryptor] before they reach disk.
 *
 * Takes a `DataStore<Preferences>` rather than a `Context` — the same trade
 * [DataStoreTripStateStore] makes — so the upgrade path from the previous build can be
 * unit-tested without an emulator.
 *
 * Only the tokens are encrypted. The server URL, the email and the two timestamps are not
 * credentials, and leaving them in the clear keeps the upgrade to a single key.
 */
class EncryptedSessionStore(
    private val dataStore: DataStore<Preferences>,
    private val cryptor: Cryptor,
) : SessionStore {
    private object Keys {
        val SERVER_URL = stringPreferencesKey("server_url")
        val EMAIL = stringPreferencesKey("email")
        val TOKEN = stringPreferencesKey("token_enc")
        val TOKEN_ISSUED_AT = longPreferencesKey("token_issued_at")
        val TOKEN_EXPIRES_AT = longPreferencesKey("token_expires_at")
        val REFRESH_TOKEN = stringPreferencesKey("refresh_token_enc")

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
            dataStore.data
                // An unreadable session file reads as logged out, for the same reason an
                // undecryptable token does: this flow is collected by ApiHolder and
                // AppNavViewModel, and letting the read failure through would cancel those
                // collectors rather than send the driver to login. Anything that is not an IO
                // failure still propagates.
                .catch { e -> if (e is IOException) emit(emptyPreferences()) else throw e }
                .map { prefs ->
                    Session(
                        serverUrl = prefs[Keys.SERVER_URL],
                        token = readToken(prefs),
                        issuedAtEpochSec = prefs[Keys.TOKEN_ISSUED_AT],
                        refreshToken = readRefreshToken(prefs),
                        accessExpiresAtEpochSec = prefs[Keys.TOKEN_EXPIRES_AT],
                        email = prefs[Keys.EMAIL],
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
     * or was replaced because it had become unreadable — is reported as no token. The flow emits
     * `token = null` and the existing launch-destination check sends the driver to login.
     *
     * This store does not delete the stored value, but that is not a promise the token is
     * recoverable: [KeystoreCryptor] regenerates the key when it cannot read the old one, so by
     * the time a decrypt fails here the ciphertext is usually already unrecoverable. It is
     * overwritten at the next login.
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

    /**
     * Read like [readToken]: a refresh token that cannot be decrypted is reported as none. The
     * access token is read on its own, so the driver stays signed in until it expires.
     */
    private fun readRefreshToken(prefs: Preferences): String? {
        val stored = prefs[Keys.REFRESH_TOKEN] ?: return null
        return try {
            cryptor.decrypt(stored)
        } catch (e: GeneralSecurityException) {
            Log.w(LOG_TAG, "Stored refresh token could not be decrypted; treating it as absent.", e)
            null
        }
    }

    override suspend fun saveLogin(serverUrl: String, email: String, tokens: IssuedTokens) =
        save(serverUrl, email, tokens)

    override suspend fun saveRefreshed(tokens: IssuedTokens) = save(serverUrl = null, email = null, tokens)

    /** Saves [tokens], and [serverUrl] and [email] unless they are null. */
    private suspend fun save(serverUrl: String?, email: String?, tokens: IssuedTokens) {
        // Encrypt before opening the transaction, so a Keystore failure reaches the caller without
        // half-writing a session: AuthRepository.login turns it into Result.failure, and
        // TokenRefresher drops the refresh token the server has just spent.
        val access = cryptor.encrypt(tokens.accessToken)
        val refresh = cryptor.encrypt(tokens.refreshToken)
        dataStore.edit { prefs ->
            if (serverUrl != null) prefs[Keys.SERVER_URL] = serverUrl
            if (email != null) prefs[Keys.EMAIL] = email
            prefs[Keys.TOKEN] = access
            prefs[Keys.REFRESH_TOKEN] = refresh
            prefs[Keys.TOKEN_ISSUED_AT] = tokens.issuedAtEpochSec
            prefs[Keys.TOKEN_EXPIRES_AT] = tokens.accessExpiresAtEpochSec
            prefs.remove(Keys.LEGACY_TOKEN)
        }
    }

    override suspend fun dropRefreshToken() {
        dataStore.edit { prefs -> prefs.remove(Keys.REFRESH_TOKEN) }
    }

    override suspend fun clearToken() {
        dataStore.edit { prefs ->
            prefs.remove(Keys.TOKEN)
            prefs.remove(Keys.TOKEN_ISSUED_AT)
            prefs.remove(Keys.REFRESH_TOKEN)
            prefs.remove(Keys.TOKEN_EXPIRES_AT)
            prefs.remove(Keys.LEGACY_TOKEN)
        }
    }
}
