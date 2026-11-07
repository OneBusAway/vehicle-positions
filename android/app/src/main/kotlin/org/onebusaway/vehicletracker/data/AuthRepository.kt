package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.LoginRequest
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import javax.inject.Inject

class AuthRepository @Inject constructor(
    private val sessionStore: SessionStore,
    private val apiFactory: ApiFactory,
    private val offlineQueue: OfflineFixQueue,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    suspend fun login(serverUrl: String, email: String, password: String): Result<Unit> = try {
        val resp = apiFactory.create(serverUrl).login(LoginRequest(email, password))
        sessionStore.saveLogin(serverUrl, email, resp.issuedAt(clock()))
        offlineQueue.claimFor("${serverUrl.trimEnd('/')} ${email.trim().lowercase()}")
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
}
