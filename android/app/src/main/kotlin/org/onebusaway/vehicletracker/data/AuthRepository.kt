package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.LoginRequest
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import retrofit2.HttpException
import java.net.UnknownServiceException
import javax.inject.Inject

class AuthRepository @Inject constructor(
    private val sessionStore: SessionStore,
    private val apiFactory: ApiFactory,
    private val apiProvider: TrackerApiProvider,
    private val offlineQueue: OfflineFixQueue,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    suspend fun login(serverUrl: String, email: String, password: String): Result<Unit> = try {
        val resp = apiFactory.create(serverUrl).login(LoginRequest(email, password))
        sessionStore.saveLogin(serverUrl, email, resp.issuedAt(clock()))
        offlineQueue.claimFor(offlineQueueAccount(serverUrl, email))
        Result.success(Unit)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapLoginError(e))
    }

    /** Tells the server this session is over. The caller signs out on this phone whatever comes back. */
    suspend fun logout(): Result<Unit> = try {
        apiProvider.get().logout()
        Result.success(Unit)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }

    /**
     * Ends the session on this phone only, for one the server has already ended: there is nothing left to log out of.
     * The server and the email stay, so signing in again asks only for the password. A write that fails is logged: the
     * driver still goes to Sign in, and the next 401 ends the session again.
     */
    suspend fun endSessionLocally() = recordLocally("the end of the session") { sessionStore.clearToken() }

    /** For the next driver: the email goes too, and the server stays. A write that fails is logged, as in [endSessionLocally]. */
    suspend fun forgetDriver() = recordLocally("the sign-out") { sessionStore.clearTokenAndEmail() }
}

private const val HTTP_NOT_FOUND = 404
private const val HTTP_METHOD_NOT_ALLOWED = 405
private const val HTTP_TOO_MANY_REQUESTS = 429

/**
 * A sign-in reads a status its own way, because a 404 or a 429 means something different here
 * than on any other call. A success that isn't our JSON reads the same as a 404. A cleartext
 * refusal has to be caught before [mapHttpError], which takes it for no network.
 */
internal fun mapLoginError(e: Exception): ApiError = when {
    e is HttpException && (e.code() == HTTP_NOT_FOUND || e.code() == HTTP_METHOD_NOT_ALLOWED) ->
        ApiError.NotATrackerServer
    e is HttpException && e.code() == HTTP_TOO_MANY_REQUESTS -> ApiError.TooManyAttempts
    e is SerializationException -> ApiError.NotATrackerServer
    e is UnknownServiceException -> ApiError.CleartextBlocked
    else -> mapHttpError(e)
}
