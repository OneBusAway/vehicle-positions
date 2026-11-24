package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.OFFLINE_QUEUE_CAPACITY
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.api.LocationHistoryDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.api.toHistoryPoint
import org.onebusaway.vehicletracker.data.offlineQueueAccount
import org.onebusaway.vehicletracker.data.tooOldToUpload
import retrofit2.HttpException
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

private const val TAG = "HistoryUploader"

/**
 * The most fixes one history upload carries, about 19 KB. The server takes up to 500, but a weak 2G signal can't get
 * that many through inside the call's timeout: the batch would be kept and start over each time, and never go.
 */
private const val HISTORY_BATCH = 80

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
        // The whole queue, as more fixes than a batch holds can be too old, and none of those is sent.
        var queued = offlineQueue.oldest(OFFLINE_QUEUE_CAPACITY)
        val expired = queued.takeWhile { tooOldToUpload(it.timestamp, nowEpochSec) }
        if (expired.isNotEmpty()) {
            offlineQueue.remove(expired)
            queued = offlineQueue.oldest(HISTORY_BATCH)
        }
        val vehicleId = queued.firstOrNull()?.vehicleId ?: return HistoryUpload.NOTHING_QUEUED
        val points = queued.take(HISTORY_BATCH).takeWhile { it.vehicleId == vehicleId }
        try {
            apiProvider.get().postLocationHistory(LocationHistoryDto(vehicleId, points.map { it.toHistoryPoint() }))
        } catch (e: HttpException) {
            if (e.code() !in PERMANENT_HISTORY_REFUSALS) {
                AppLog.w(TAG, "Keeping queued fixes after HTTP ${e.code()}", e)
                return HistoryUpload.KEPT
            }
            AppLog.w(TAG, "Dropping ${points.size} queued fixes after HTTP ${e.code()}", e)
        } catch (e: IOException) {
            AppLog.w(TAG, "Keeping queued fixes after network failure", e)
            return HistoryUpload.KEPT
        }
        offlineQueue.remove(points)
        return HistoryUpload.BATCH_SENT
    }
}
