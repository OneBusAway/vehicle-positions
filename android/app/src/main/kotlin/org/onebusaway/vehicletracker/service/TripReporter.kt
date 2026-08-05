package org.onebusaway.vehicletracker.service

import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApi
import retrofit2.HttpException
import java.io.IOException
import javax.inject.Inject

data class LocationFix(
    val latitude: Double,
    val longitude: Double,
    val bearing: Double?,
    val speed: Double?,
    val accuracy: Double?,
    val timeEpochSec: Long,
)

private const val CLOCK_SKEW_THRESHOLD = 3

class TripReporter @Inject constructor(
    private val api: TrackerApi,
    private val tracking: TrackingRepository,
) {
    private var gpsAvailable = true
    private var consecutiveTimestampRejects = 0
    private var currentSendProblem = TrackingProblem.NONE

    fun gpsAvailable(available: Boolean) {
        gpsAvailable = available
        refreshProblem(sendProblem = currentSendProblem)
    }

    suspend fun report(trip: ActiveTrip, fix: LocationFix) {
        val dto = LocationReportDto(
            vehicleId = trip.vehicleId,
            tripId = trip.locationTripId,
            latitude = fix.latitude,
            longitude = fix.longitude,
            bearing = fix.bearing?.takeIf { it in 0.0..360.0 },
            speed = fix.speed?.coerceAtLeast(0.0),
            accuracy = fix.accuracy,
            timestamp = fix.timeEpochSec,
        )
        try {
            api.postLocation(dto)
            consecutiveTimestampRejects = 0
            tracking.update { it.copy(fixesSent = it.fixesSent + 1) }
            refreshProblem(sendProblem = TrackingProblem.NONE)
        } catch (e: HttpException) {
            when {
                e.code() == 401 -> refreshProblem(sendProblem = TrackingProblem.AUTH_EXPIRED)
                e.code() == 429 -> Unit // rate-limited: drop silently, keep current status
                e.code() == 400 && e.response()?.errorBody()?.string()?.contains("timestamp") == true -> {
                    consecutiveTimestampRejects++
                    if (consecutiveTimestampRejects >= CLOCK_SKEW_THRESHOLD) {
                        refreshProblem(sendProblem = TrackingProblem.CLOCK_SKEW)
                    }
                }
                else -> Unit // other 4xx/5xx: log-and-drop per spec, keep current status
            }
        } catch (e: IOException) {
            refreshProblem(sendProblem = TrackingProblem.NO_NETWORK)
        }
    }

    private fun refreshProblem(sendProblem: TrackingProblem) {
        currentSendProblem = sendProblem
        val effective = if (!gpsAvailable) TrackingProblem.NO_GPS else sendProblem
        tracking.update { it.copy(problem = effective) }
    }
}
