package org.onebusaway.vehicletracker.service

import android.util.Log
import kotlinx.coroutines.flow.first
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.OccupancyStatus
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.ElapsedMillisClock
import retrofit2.HttpException
import java.io.IOException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import javax.inject.Inject

private const val TAG = "TripReporter"

data class LocationFix(
    val latitude: Double,
    val longitude: Double,
    val bearing: Double?,
    val speed: Double?,
    val accuracy: Double?,
    val timeEpochSec: Long,
)

private const val CLOCK_SKEW_THRESHOLD = 3

/**
 * How far apart [LocationTrackingService] asks for fixes when no screen needs them faster. Each
 * of those fixes is a report, so this is also how often the server hears from the vehicle.
 */
const val REPORT_INTERVAL_MS = 10_000L

/**
 * The least time between two reports. It is well under [REPORT_INTERVAL_MS] on purpose: Android
 * hands over a fix up to a tenth of the interval early, so fixes asked for ten seconds apart
 * arrive about nine seconds apart. A threshold at ten, or even at nine, would hold back every
 * other one of those and halve the report rate.
 */
private const val MIN_REPORT_GAP_MS = 8_500L

private const val NEVER_REPORTED = Long.MIN_VALUE

class TripReporter @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val tracking: TrackingRepository,
    @param:ElapsedMillisClock private val elapsedMs: () -> Long,
    private val offlineQueue: OfflineFixQueue,
    private val historyUploader: HistoryUploader,
    private val backlogScheduler: BacklogUploadScheduler,
    private val tripStateStore: TripStateStore,
) {
    private val lastReportAt = AtomicLong(NEVER_REPORTED)
    private val reportInFlight = AtomicBoolean(false)
    private var gpsAvailable = true
    private var consecutiveTimestampRejects = 0
    private var currentSendProblem = TrackingProblem.NONE

    fun gpsAvailable(available: Boolean) {
        gpsAvailable = available
        refreshProblem(sendProblem = currentSendProblem)
    }

    /**
     * Sends [fix], unless a report went out less than [MIN_REPORT_GAP_MS] ago. While the tracking
     * screen is open the service takes a fix every second for the display; the server still hears
     * from the vehicle as often as it does with the screen off, and no oftener. A fix held back
     * changes nothing the driver sees.
     *
     * Only one report is ever on its way, as on iOS. On a slow network a second one could
     * otherwise overtake the first, and the server, which keeps whatever arrived last, would be
     * left with the older position. A fix that arrives meanwhile is dropped rather than queued:
     * by the time it could go it would be stale, and the next one is seconds behind it.
     *
     * A report that goes through is followed by one batch of the fixes queued while there was no
     * connection, unless [BacklogUploadJob] is uploading one. That upload is not part of the
     * report: a slow one would otherwise hold back the live fixes behind it, and those are
     * dropped, not queued. When the batch goes and more is queued, the job is scheduled afresh to
     * take the rest, unless it is running.
     */
    suspend fun report(trip: ActiveTrip, fix: LocationFix) {
        if (!reportInFlight.compareAndSet(false, true)) return
        val sent = try {
            send(trip, fix)
        } finally {
            reportInFlight.set(false)
        }
        // The connection works again, and this report has gone: send one batch of what queued up
        // while it did not. At most one per report, and one at a time, keeps within the server's
        // limit on history uploads; a report that goes through while a batch is on its way starts none.
        if (!sent) return
        try {
            val upload = historyUploader.uploadIfIdle(nowEpochSec = fix.timeEpochSec)
            // The server has just answered an upload, so a job put off after earlier failures, by hours
            // in the end, has nothing left to wait for. A running one would be stopped, so it is left alone.
            if (upload == HistoryUpload.BATCH_SENT && !historyUploader.jobRunning && offlineQueue.oldest(1).isNotEmpty()) {
                backlogScheduler.scheduleAfresh()
            }
        } catch (e: IOException) {
            // The upload's own network failures are handled inside; this is the queue's file failing.
            Log.w(TAG, "Could not read or update the offline queue", e)
        }
    }

    /** Returns whether the server took [fix]. */
    private suspend fun send(trip: ActiveTrip, fix: LocationFix): Boolean {
        if (!reportIsDue()) return false
        val dto = LocationReportDto(
            vehicleId = trip.vehicleId,
            tripId = trip.gtfsTripId.ifBlank { null },
            routeId = trip.routeId,
            startDate = trip.startDate,
            latitude = fix.latitude,
            longitude = fix.longitude,
            bearing = fix.bearing?.takeIf { it in 0.0..360.0 },
            speed = fix.speed?.coerceAtLeast(0.0),
            accuracy = fix.accuracy,
            timestamp = fix.timeEpochSec,
            occupancyStatus = occupancy(trip),
        )
        try {
            apiProvider.get().postLocation(dto)
            consecutiveTimestampRejects = 0
            tracking.update { it.copy(fixesSent = it.fixesSent + 1) }
            refreshProblem(sendProblem = TrackingProblem.NONE)
        } catch (e: HttpException) {
            val is400WithTimestamp = e.code() == 400 &&
                runCatching { e.response()?.errorBody()?.string() }
                    .getOrNull()
                    ?.contains("timestamp") == true
            when {
                e.code() == 401 -> refreshProblem(sendProblem = TrackingProblem.AUTH_EXPIRED)
                e.code() == 429 -> Unit // rate-limited: drop silently, keep current status
                // Not assigned, vehicle deactivated (403), or no active trip / a trip mismatch (409):
                // every report until dispatch fixes it is refused, so say so rather than look healthy.
                e.code() == 403 || e.code() == 409 -> {
                    Log.w(TAG, "Location report refused with HTTP ${e.code()}", e)
                    refreshProblem(sendProblem = TrackingProblem.REPORTS_REFUSED)
                }
                is400WithTimestamp -> {
                    consecutiveTimestampRejects++
                    if (consecutiveTimestampRejects >= CLOCK_SKEW_THRESHOLD) {
                        refreshProblem(sendProblem = TrackingProblem.CLOCK_SKEW)
                    }
                }
                else -> Log.w(TAG, "Dropping location report after HTTP ${e.code()}", e) // other 4xx/5xx: log-and-drop per spec, keep current status
            }
            return false
        } catch (e: IOException) {
            // Too late for the realtime feed by the time it could go, but it is still where the bus
            // was: kept for location history.
            Log.w(TAG, "Queueing location report for history after network failure", e)
            try {
                offlineQueue.add(dto)
                backlogScheduler.schedule()
            } catch (queueError: IOException) {
                // A full disk, say. Nothing above this catches it, so it would end tracking mid-trip:
                // losing one queued fix is the lesser loss.
                Log.w(TAG, "Could not queue location report for history", queueError)
            }
            refreshProblem(sendProblem = TrackingProblem.NO_NETWORK)
            return false
        }
        return true
    }

    /**
     * How full the driver says the vehicle is on [trip], as the trip is stored now: the driver can change it at any time
     * after the service read the trip. Null once the trip is no longer the stored one.
     */
    private suspend fun occupancy(trip: ActiveTrip): OccupancyStatus? = try {
        tripStateStore.activeTrip.first()?.takeIf { it.tripDbId == trip.tripDbId }?.occupancy
    } catch (e: IOException) {
        // Nothing above this catches it, so it would end tracking mid-trip: the report goes without it instead.
        Log.w(TAG, "Could not read the trip's occupancy", e)
        null
    }

    /** Claims the next report slot. */
    private fun reportIsDue(): Boolean {
        val now = elapsedMs()
        val last = lastReportAt.get()
        if (last != NEVER_REPORTED && now - last < MIN_REPORT_GAP_MS) return false
        lastReportAt.set(now)
        return true
    }

    private fun refreshProblem(sendProblem: TrackingProblem) {
        currentSendProblem = sendProblem
        val effective = if (!gpsAvailable) TrackingProblem.NO_GPS else sendProblem
        tracking.update { it.copy(problem = effective) }
    }
}
