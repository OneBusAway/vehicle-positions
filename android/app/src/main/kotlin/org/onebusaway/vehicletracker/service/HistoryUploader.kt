package org.onebusaway.vehicletracker.service

import android.util.Log
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.api.LocationHistoryDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.api.toHistoryPoint
import org.onebusaway.vehicletracker.data.offlineQueueAccount
import retrofit2.HttpException
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

private const val TAG = "HistoryUploader"

/** The most fixes one history upload carries; the server takes at most 500. */
private const val HISTORY_BATCH = 500

/**
 * How old a queued fix may be and still be uploaded: the server takes a day, less a margin for the
 * phone's clock and the time the upload takes.
 */
private const val MAX_HISTORY_AGE_SEC = 24 * 3600L - 600L

/** Refusals a history upload would get again however often it were retried. */
private val PERMANENT_HISTORY_REFUSALS = setOf(400, 403, 404)

/** What one [HistoryUploader] call did with the queue. */
enum class HistoryUpload {
    /** Nothing was queued, once fixes too old to upload were dropped. */
    NOTHING_QUEUED,

    /** One batch left the queue: the server took it, or refused it for good. */
    BATCH_SENT,

    /** The batch failed for now and stays queued. */
    KEPT,

    /** Nobody is signed in to the account the fixes were queued under, so they stay queued. */
    NOT_SIGNED_IN,
}

/**
 * Uploads the fixes [OfflineFixQueue] holds, one batch per call, for both [TripReporter] and
 * [BacklogUploadJob]. One upload runs at a time across the two: a batch stays at the front of the
 * queue until it has gone, so two uploads at once would both send it.
 */
@Singleton
class HistoryUploader @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val sessionStore: SessionStore,
    private val offlineQueue: OfflineFixQueue,
) {
    private val lock = Mutex()

    /**
     * Whether [BacklogUploadJob] is working through the queue, the pauses between its batches included. The job
     * says so here because JobScheduler, before Android 14, can't tell a job that is running from one that is waiting.
     */
    @Volatile
    var jobRunning = false

    /** Uploads one batch, unless another upload is on its way: null then. */
    suspend fun uploadIfIdle(nowEpochSec: Long): HistoryUpload? {
        if (!lock.tryLock()) return null
        try {
            return uploadOneBatch(nowEpochSec)
        } finally {
            lock.unlock()
        }
    }

    /** Uploads one batch, after any upload on its way. */
    suspend fun upload(nowEpochSec: Long): HistoryUpload = lock.withLock { uploadOneBatch(nowEpochSec) }

    /**
     * Uploads the oldest queued fixes for one vehicle. A batch the server refuses for good (bad
     * data, a vehicle no longer assigned, a server without the endpoint) is dropped; one that
     * fails for now (no connection, an expired sign-in, the rate limit, a server error) stays for
     * the next try.
     *
     * @throws IOException when the queue's file can't be read or updated.
     */
    private suspend fun uploadOneBatch(nowEpochSec: Long): HistoryUpload {
        // The fixes go with whatever token and server the app has now. Sign in saves a new driver's
        // session before it drops the fixes queued under the last one, so the two must match here.
        val session = sessionStore.session.first()
        val serverUrl = session.serverUrl
        val email = session.email
        if (session.token == null || serverUrl == null || email == null ||
            offlineQueue.owner() != offlineQueueAccount(serverUrl, email)
        ) {
            return HistoryUpload.NOT_SIGNED_IN
        }
        var batch = offlineQueue.oldest(HISTORY_BATCH)
        val expired = batch.takeWhile { nowEpochSec - it.timestamp > MAX_HISTORY_AGE_SEC }.size
        if (expired > 0) {
            offlineQueue.remove(batch.take(expired))
            batch = offlineQueue.oldest(HISTORY_BATCH)
        }
        val vehicleId = batch.firstOrNull()?.vehicleId ?: return HistoryUpload.NOTHING_QUEUED
        val points = batch.takeWhile { it.vehicleId == vehicleId }
        try {
            apiProvider.get().postLocationHistory(LocationHistoryDto(vehicleId, points.map { it.toHistoryPoint() }))
        } catch (e: HttpException) {
            if (e.code() !in PERMANENT_HISTORY_REFUSALS) {
                Log.w(TAG, "Keeping queued fixes after HTTP ${e.code()}", e)
                return HistoryUpload.KEPT
            }
            Log.w(TAG, "Dropping ${points.size} queued fixes after HTTP ${e.code()}", e)
        } catch (e: IOException) {
            Log.w(TAG, "Keeping queued fixes after network failure", e)
            return HistoryUpload.KEPT
        }
        offlineQueue.remove(points)
        return HistoryUpload.BATCH_SENT
    }
}
