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
        sessionStore.saveLogin(serverUrl, resp.issuedAt(clock()))
        offlineQueue.claimFor("${serverUrl.trimEnd('/')} ${email.trim().lowercase()}")
        Result.success(Unit)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }
}
