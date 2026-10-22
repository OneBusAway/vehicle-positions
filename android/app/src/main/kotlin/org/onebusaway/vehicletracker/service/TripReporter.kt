package org.onebusaway.vehicletracker.service

import android.util.Log
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.api.LocationHistoryDto
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.api.toHistoryPoint
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

/** The most fixes one history upload carries; the server takes at most 500. */
private const val HISTORY_BATCH = 500

/**
 * How old a queued fix may be and still be uploaded: the server takes a day, less a margin for the
 * phone's clock and the time the upload takes.
 */
private const val MAX_HISTORY_AGE_SEC = 24 * 3600L - 600L

/** Refusals a history upload would get again however often it were retried. */
private val PERMANENT_HISTORY_REFUSALS = setOf(400, 403, 404)

class TripReporter @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val tracking: TrackingRepository,
    @param:ElapsedMillisClock private val elapsedMs: () -> Long,
    private val offlineQueue: OfflineFixQueue,
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
     */
    suspend fun report(trip: ActiveTrip, fix: LocationFix) {
        if (!reportInFlight.compareAndSet(false, true)) return
        try {
            send(trip, fix)
        } finally {
            reportInFlight.set(false)
        }
    }

    private suspend fun send(trip: ActiveTrip, fix: LocationFix) {
        if (!reportIsDue()) return
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
                is400WithTimestamp -> {
                    consecutiveTimestampRejects++
                    if (consecutiveTimestampRejects >= CLOCK_SKEW_THRESHOLD) {
                        refreshProblem(sendProblem = TrackingProblem.CLOCK_SKEW)
                    }
                }
                else -> Log.w(TAG, "Dropping location report after HTTP ${e.code()}", e) // other 4xx/5xx: log-and-drop per spec, keep current status
            }
            return
        } catch (e: IOException) {
            // Too late for the realtime feed by the time it could go, but it is still where the bus
            // was: kept for location history.
            Log.w(TAG, "Queueing location report for history after network failure", e)
            offlineQueue.add(dto)
            refreshProblem(sendProblem = TrackingProblem.NO_NETWORK)
            return
        }
        // The connection works again, and this report has gone: send one batch of what queued up
        // while it did not. One per report keeps within the server's limit on history uploads.
        uploadOneHistoryBatch(nowEpochSec = fix.timeEpochSec)
    }

    /**
     * Uploads the oldest queued fixes for one vehicle. A batch the server refuses for good (bad
     * data, a vehicle no longer assigned, a server without the endpoint) is dropped; one that
     * fails for now (no connection, an expired sign-in, the rate limit, a server error) stays for
     * the next report to try.
     */
    private suspend fun uploadOneHistoryBatch(nowEpochSec: Long) {
        var batch = offlineQueue.oldest(HISTORY_BATCH)
        val expired = batch.takeWhile { nowEpochSec - it.timestamp > MAX_HISTORY_AGE_SEC }.size
        if (expired > 0) {
            offlineQueue.removeOldest(expired)
            batch = offlineQueue.oldest(HISTORY_BATCH)
        }
        val vehicleId = batch.firstOrNull()?.vehicleId ?: return
        val points = batch.takeWhile { it.vehicleId == vehicleId }
        try {
            apiProvider.get().postLocationHistory(LocationHistoryDto(vehicleId, points.map { it.toHistoryPoint() }))
            offlineQueue.removeOldest(points.size)
        } catch (e: HttpException) {
            if (e.code() in PERMANENT_HISTORY_REFUSALS) {
                Log.w(TAG, "Dropping ${points.size} queued fixes after HTTP ${e.code()}", e)
                offlineQueue.removeOldest(points.size)
            } else {
                Log.w(TAG, "Keeping queued fixes after HTTP ${e.code()}", e)
            }
        } catch (e: IOException) {
            Log.w(TAG, "Keeping queued fixes after network failure", e)
        }
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
