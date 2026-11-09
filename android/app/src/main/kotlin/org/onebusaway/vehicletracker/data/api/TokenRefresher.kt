package org.onebusaway.vehicletracker.data.api

import android.util.Log
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.SerializationException
import okhttp3.Authenticator
import okhttp3.Request
import okhttp3.Response
import okhttp3.Route
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.issuedAt
import java.io.IOException
import java.security.GeneralSecurityException

private const val TAG = "TokenRefresher"

/**
 * How long an early trade that was refused or failed holds back the next early one. Drivers at a
 * depot share one address, and with it the server's per-IP refresh limit, which a retry on every
 * report would spend.
 */
private const val EARLY_RETRY_DELAY_SEC = 60L

/**
 * Trades the stored refresh token for a new access token, for the client [ApiFactory] builds.
 *
 * One trade at a time, starting from the stored session. The server takes a second use of a
 * refresh token for theft and revokes every refresh token the driver holds, so two requests
 * refreshing together would sign the driver out. Each trade runs under one lock and begins by
 * reading the session from [sessionStore], never from a cached copy such as `ApiHolder`'s: that
 * copy can lag a save, and a lagging copy would present a token the server has already spent.
 * When the stored access token is no longer the one the caller sent, another request has
 * refreshed in the meantime, and the caller gets the new token without a call to the server.
 *
 * It blocks, holding the lock across the call to the server. OkHttp calls it on its own threads,
 * where that is fine, and a request that waits is one that would otherwise have refreshed again.
 *
 * [cachedRefreshDueAt] is `ApiHolder`'s copy of [Session.refreshDueAtEpochSec], read on every
 * request to skip the lock while no refresh is due. It only decides whether to look: whether to
 * refresh is decided from the store.
 */
class TokenRefresher(
    private val sessionStore: SessionStore,
    private val clock: () -> Long,
    private val cachedRefreshDueAt: () -> Long?,
    private val refreshApi: (serverUrl: String) -> RefreshApi,
) {
    private val lock = Any()

    /**
     * The refresh token whose early trade was refused or failed, and until when no early trade
     * presents it again. Guarded by [lock]. A sign-in, or a trade that works, stores another
     * refresh token, so either one ends the hold.
     */
    private var earlyTradesHeld: EarlyTradesHeld? = null

    private class EarlyTradesHeld(val refreshToken: String, val untilEpochSec: Long)

    /**
     * Before a request goes out with [currentToken]: a new token when the current one is about to
     * expire, otherwise [currentToken]. An early refresh that fails or is refused never fails the
     * request, which goes out with [currentToken] while it still works; the 401 that follows its
     * expiry is where a refused refresh token is dropped and a failure is reported. It does hold
     * back the next early refresh for a minute, though never the refresh after a 401.
     */
    fun beforeExpiry(currentToken: String): String {
        if (!isDue(cachedRefreshDueAt())) return currentToken
        synchronized(lock) {
            val stored = storedSession()
            val current = stored.token ?: return currentToken
            // A token another request has just refreshed is not due, so it goes out as it is.
            if (!isDue(stored.refreshDueAtEpochSec)) return current
            val refreshToken = stored.refreshToken ?: return current
            if (earlyTradesAreHeld(refreshToken)) return current
            val serverUrl = stored.serverUrl ?: return current
            return try {
                trade(serverUrl, refreshToken) ?: current.also {
                    Log.w(TAG, "The server refused an early refresh; sending the request with the current token.")
                    holdEarlyTrades(refreshToken)
                }
            } catch (e: IOException) {
                Log.w(TAG, "Early refresh failed; sending the request with the current token.", e)
                holdEarlyTrades(refreshToken)
                current
            }
        }
    }

    private fun earlyTradesAreHeld(refreshToken: String): Boolean {
        val held = earlyTradesHeld ?: return false
        return held.refreshToken == refreshToken && clock() < held.untilEpochSec
    }

    private fun holdEarlyTrades(refreshToken: String) {
        earlyTradesHeld = EarlyTradesHeld(refreshToken, clock() + EARLY_RETRY_DELAY_SEC)
    }

    private fun isDue(refreshDueAt: Long?) = refreshDueAt != null && clock() >= refreshDueAt

    /**
     * After a request sent with [failedToken] got a 401: the token to send it again with, or null
     * when there is none and the 401 stands. A refused refresh token is dropped and the access
     * token kept until it fails on its own, so the next 401 reaches the caller, which asks the
     * driver to sign in.
     *
     * @throws IOException when the server can't be reached or can't refresh right now. Both
     *   tokens are kept, so the next request tries again.
     */
    @Throws(IOException::class)
    fun afterUnauthorized(failedToken: String): String? {
        synchronized(lock) {
            val stored = storedSession()
            val current = stored.token ?: return null
            if (current != failedToken) return current
            val refreshToken = stored.refreshToken ?: return null
            val serverUrl = stored.serverUrl ?: return null
            val refreshed = trade(serverUrl, refreshToken)
            if (refreshed == null) {
                Log.w(TAG, "The server refused the refresh token; the driver signs in again at the next 401.")
                dropRefreshToken()
            }
            return refreshed
        }
    }

    private fun storedSession(): Session = runBlocking { sessionStore.session.first() }

    /**
     * Presents [refreshToken] once and saves the pair the server hands back. Returns the new
     * access token, or null when the server refused the refresh token.
     *
     * @throws IOException when the server can't be reached or answers anything else.
     */
    private fun trade(serverUrl: String, refreshToken: String): String? {
        val reply = try {
            refreshApi(serverUrl).refresh(RefreshRequest(refreshToken)).execute()
        } catch (e: SerializationException) {
            // A 200 that is not a token pair, such as a captive portal's page. The server may
            // never have seen the refresh token, so it is kept for the next request.
            throw IOException("The refresh reply could not be read", e)
        }
        val issued = reply.body()
        return when {
            reply.isSuccessful && issued != null -> {
                save(issued.issuedAt(clock()), spentRefreshToken = refreshToken)
                issued.accessToken
            }
            reply.code() == 401 -> null
            else -> throw IOException("Refresh failed with HTTP ${reply.code()}")
        }
    }

    /**
     * The server spent the old refresh token when it answered, so a pair that can't be saved costs
     * the old one too: presented again, it would revoke every refresh token the driver holds. The
     * new access token is still returned, for the request that is waiting on it.
     */
    private fun save(tokens: IssuedTokens, spentRefreshToken: String) {
        try {
            runBlocking { sessionStore.saveRefreshed(tokens, spentRefreshToken) }
        } catch (e: GeneralSecurityException) {
            Log.w(TAG, "Could not encrypt the refreshed tokens; dropping the spent refresh token.", e)
            dropRefreshToken()
        } catch (e: IOException) {
            Log.w(TAG, "Could not save the refreshed tokens; dropping the spent refresh token.", e)
            dropRefreshToken()
        }
    }

    private fun dropRefreshToken() {
        try {
            runBlocking { sessionStore.dropRefreshToken() }
        } catch (e: IOException) {
            Log.w(TAG, "Could not drop the refresh token.", e)
        }
    }
}

/**
 * Refreshes after a 401 and sends the request again, once. A request that went out without an
 * access token, such as a sign-in, is left alone: a wrong password must not quietly refresh the
 * session of whoever signed in on this phone before.
 */
class TokenAuthenticator(private val refresher: TokenRefresher) : Authenticator {
    override fun authenticate(route: Route?, response: Response): Request? {
        val sent = response.request.header("Authorization")?.removePrefix("Bearer ") ?: return null
        if (response.priorResponse?.code == 401) return null
        val token = refresher.afterUnauthorized(sent) ?: return null
        // The request goes again without passing AuthInterceptor, so the header is set here.
        return response.request.newBuilder().header("Authorization", "Bearer $token").build()
    }
}
