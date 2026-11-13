package org.onebusaway.vehicletracker.data

import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import kotlinx.serialization.SerializationException
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.LoginRequest
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.service.BacklogUploadScheduler
import retrofit2.HttpException
import java.io.IOException
import java.net.UnknownServiceException
import javax.inject.Inject

private const val TAG = "AuthRepository"

class AuthRepository @Inject constructor(
    private val sessionStore: SessionStore,
    private val tripStateStore: TripStateStore,
    private val apiFactory: ApiFactory,
    private val apiProvider: TrackerApiProvider,
    private val offlineQueue: OfflineFixQueue,
    private val backlogScheduler: BacklogUploadScheduler,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    suspend fun login(serverUrl: String, email: String, password: String): Result<Unit> = try {
        val resp = apiFactory.create(serverUrl).login(LoginRequest(email, password))
        dropTripFromAnotherServer(serverUrl)
        sessionStore.saveLogin(serverUrl, email, resp.issuedAt(clock()))
        offlineQueue.claimFor(offlineQueueAccount(serverUrl, email))
        scheduleBacklogUpload()
        Result.success(Unit)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapLoginError(e))
    }

    /**
     * The fixes left in the queue now are this driver's. A backlog job that ran while nobody was signed in to them
     * gave up and won't run again until another fix is queued, which may be the next trip's.
     */
    private suspend fun scheduleBacklogUpload() {
        try {
            if (offlineQueue.oldest(1).isNotEmpty()) backlogScheduler.schedule()
        } catch (e: IOException) {
            // The sign-in has worked. The next live report still uploads a batch.
            Log.w(TAG, "Could not read the offline queue", e)
        }
    }

    /**
     * A trip stored for another server stays open there, and would otherwise be picked up here: Resume would send its
     * reports to [serverUrl]. Dropped once the sign-in has worked, and before it is saved, so the new token never sits
     * next to it. The old server's admin can end it there. Not kept as an end to send, which would go here too.
     */
    private suspend fun dropTripFromAnotherServer(serverUrl: String) {
        val trip = tripStateStore.activeTrip.first() ?: return
        val tripServer = trip.server(sessionStore.session.first().serverUrl) ?: return
        if (sameServer(tripServer, serverUrl)) return
        Log.i(TAG, "Dropped trip ${trip.tripDbId}: it is open on another server than the one signed in to")
        tripStateStore.clearActiveTrip()
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
