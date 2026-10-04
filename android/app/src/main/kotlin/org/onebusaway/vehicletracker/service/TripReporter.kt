package org.onebusaway.vehicletracker.service

import android.util.Log
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.ElapsedMillisClock
import retrofit2.HttpException
import java.io.IOException
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
) {
    private val lastReportAt = AtomicLong(NEVER_REPORTED)
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
     */
    suspend fun report(trip: ActiveTrip, fix: LocationFix) {
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
        } catch (e: IOException) {
            Log.w(TAG, "Dropping location report after network failure", e)
            refreshProblem(sendProblem = TrackingProblem.NO_NETWORK)
        }
    }

    /** Claims the next report slot. Atomic, because the service launches each report on its own. */
    private fun reportIsDue(): Boolean {
        val now = elapsedMs()
        val last = lastReportAt.get()
        if (last != NEVER_REPORTED && now - last < MIN_REPORT_GAP_MS) return false
        return lastReportAt.compareAndSet(last, now)
    }

    private fun refreshProblem(sendProblem: TrackingProblem) {
        currentSendProblem = sendProblem
        val effective = if (!gpsAvailable) TrackingProblem.NO_GPS else sendProblem
        tracking.update { it.copy(problem = effective) }
    }
}
