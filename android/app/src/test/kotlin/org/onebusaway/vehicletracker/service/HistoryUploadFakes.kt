package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.CompletableDeferred
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.LocationHistoryDto
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApi
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.offlineQueueAccount

/** The driver signed in on the phone, who owns the queued fixes unless a test says otherwise. */
val signedInDriver = Session("http://localhost/", "jwt", 0L, refreshToken = "refresh", email = "driver@example.com")

val signedInAccount = offlineQueueAccount(signedInDriver.serverUrl!!, signedInDriver.email!!)

/**
 * A [HistoryUploader] for [signedInDriver]. [queue] is handed to that driver as a sign-in would, unless it already has
 * an owner.
 */
suspend fun uploaderFor(
    api: TrackerApi,
    queue: OfflineFixQueue,
    sessions: FakeSessionStore = FakeSessionStore().apply { state.value = signedInDriver },
): HistoryUploader {
    queue.claimIfUnowned(signedInAccount)
    return HistoryUploader(TrackerApiProvider { api }, sessions, queue)
}

/** Counts what was asked of the backlog job. */
class FakeBacklogUploadScheduler : BacklogUploadScheduler {
    var scheduled = 0
    var scheduledAfresh = 0
    override fun schedule() { scheduled++ }
    override fun scheduleAfresh() { scheduledAfresh++ }
}

/**
 * Takes each live report at once, and holds each history upload until [finishUploads] completes. [historyFailure]
 * picks what the upload with that index (from 0) throws once it is let through, or null when it goes through.
 */
class SlowHistoryApi(
    unused: TrackerApi = ApiFactory { "jwt" }.create("http://127.0.0.1/"),
) : TrackerApi by unused {
    var liveFailure: Exception? = null
    var historyFailure: (upload: Int) -> Exception? = { null }
    var liveReports = 0
    val uploads = mutableListOf<String>()
    val finishUploads = CompletableDeferred<Unit>()

    override suspend fun postLocation(body: LocationReportDto) {
        liveFailure?.let { throw it }
        liveReports++
    }

    override suspend fun postLocationHistory(body: LocationHistoryDto) {
        val upload = uploads.size
        uploads += body.vehicleId
        finishUploads.await()
        historyFailure(upload)?.let { throw it }
    }
}

/** A fix queued for [vehicleId]. Each test vehicle has one, so each upload carries one fix and names it. */
fun queuedFix(vehicleId: String, timestamp: Long = 1000L) =
    LocationReportDto(vehicleId = vehicleId, routeId = "5", startDate = "20260804", latitude = -1.29, longitude = 36.82, timestamp = timestamp)
