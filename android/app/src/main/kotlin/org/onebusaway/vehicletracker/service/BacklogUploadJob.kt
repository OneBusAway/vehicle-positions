package org.onebusaway.vehicletracker.service

import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context
import android.util.Log
import dagger.hilt.android.AndroidEntryPoint
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelChildren
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import java.io.IOException
import javax.inject.Inject

private const val TAG = "BacklogUploadJob"

private const val BACKLOG_UPLOAD_JOB_ID = 1

/** How far apart the job sends batches: the server takes one history upload per driver every five seconds. */
internal const val HISTORY_UPLOAD_INTERVAL_MS = 5_000L

/** Asks for the queued fixes to be uploaded once there is a connection, whether or not a trip is running. */
interface BacklogUploadScheduler {
    /** For a fix that has just been queued. A job that is waiting or running will get to it, and is left as it is. */
    fun schedule()

    /**
     * For when the server has just answered an upload. JobScheduler makes a job that keeps failing wait longer each
     * time, in the end for hours, and [schedule] leaves that wait in place. This puts a new job in its place, which
     * starts with no wait. It also stops a job that is running, so the caller checks [HistoryUploader.jobRunning].
     */
    fun scheduleAfresh()
}

class JobBacklogUploadScheduler @Inject constructor(
    @param:ApplicationContext private val context: Context,
) : BacklogUploadScheduler {
    private val jobs: JobScheduler get() = context.getSystemService(JobScheduler::class.java)

    override fun schedule() {
        // Scheduling again would stop a job that is already uploading, and one that is waiting
        // will pick up this fix too.
        if (jobs.getPendingJob(BACKLOG_UPLOAD_JOB_ID) != null) return
        scheduleAfresh()
    }

    override fun scheduleAfresh() {
        val job = JobInfo.Builder(BACKLOG_UPLOAD_JOB_ID, ComponentName(context, BacklogUploadJob::class.java))
            .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
            .setPersisted(true)
            .build()
        try {
            if (jobs.schedule(job) != JobScheduler.RESULT_SUCCESS) Log.w(TAG, "Could not schedule the backlog upload")
        } catch (e: IllegalStateException) {
            // Past the limits on how many jobs an app may schedule, and how often. The next live report
            // still uploads a batch.
            Log.w(TAG, "Could not schedule the backlog upload", e)
        }
    }
}

/** Uploads the whole backlog, a batch at a time, for [BacklogUploadJob]. */
class BacklogDrain @Inject constructor(
    private val uploader: HistoryUploader,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    /** Returns whether to try again later: true when fixes are left that failed only for now. */
    suspend fun run(): Boolean {
        uploader.jobRunning = true
        try {
            return uploadAll()
        } finally {
            uploader.jobRunning = false
        }
    }

    private suspend fun uploadAll(): Boolean {
        while (true) {
            val outcome = try {
                uploader.upload(nowEpochSec = clock())
            } catch (e: IOException) {
                // The upload's own network failures are handled inside; this is the queue's file failing.
                Log.w(TAG, "Could not read or update the offline queue", e)
                return false
            }
            when (outcome) {
                HistoryUpload.BATCH_SENT -> delay(HISTORY_UPLOAD_INTERVAL_MS)
                HistoryUpload.KEPT -> return true
                HistoryUpload.NOTHING_QUEUED, HistoryUpload.NOT_SIGNED_IN -> return false
            }
        }
    }
}

/**
 * Uploads the fixes queued while there was no connection as soon as there is one. Without it they
 * waited for a live report to go through, so a dead zone at the end of a shift waited for the next
 * trip, and fixes over a day old by then were dropped.
 */
@AndroidEntryPoint
class BacklogUploadJob : JobService() {
    @Inject lateinit var drain: BacklogDrain

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onStartJob(params: JobParameters): Boolean {
        scope.launch { jobFinished(params, drain.run()) }
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean {
        scope.coroutineContext.cancelChildren()
        return true
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }
}
