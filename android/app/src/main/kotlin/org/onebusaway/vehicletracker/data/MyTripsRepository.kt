package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.MyTripDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import java.time.Instant
import java.time.OffsetDateTime
import javax.inject.Inject

private const val TAG = "MyTripsRepository"

/** One of the driver's own trips, as My trips shows it. [route] and [headsign] are the schedule's as it is now. */
data class MyTrip(
    val id: Long,
    val vehicleId: String,
    /** Empty for a trip started with its GTFS trip alone. */
    val routeId: String,
    /** Null when the schedule no longer has the trip's route. */
    val route: MyTripRoute?,
    /** Empty when the schedule gives none. */
    val headsign: String,
    val start: Instant,
    /** Null while the trip runs. */
    val end: Instant?,
)

data class MyTripRoute(val shortName: String, val longName: String, val color: String, val textColor: String)

/** A page of the driver's own trips, newest first. */
data class MyTripsPage(val hasMore: Boolean, val trips: List<MyTrip>)

/** The driver's own trips, a page at a time. */
fun interface MyTripsSource {
    suspend fun page(limit: Int, offset: Int): Result<MyTripsPage>
}

/** `GET /api/v1/trips`, with the failures as the pickers' loads give them. */
class MyTripsRepository @Inject constructor(private val apiProvider: TrackerApiProvider) : MyTripsSource {
    override suspend fun page(limit: Int, offset: Int): Result<MyTripsPage> = try {
        val page = apiProvider.get().myTrips(limit, offset)
        Result.success(MyTripsPage(page.hasMore, page.trips.map { it.toTrip() }))
    } catch (e: CancellationException) {
        // The driver left the page while it loaded. Nothing failed, so nothing is logged (Aaron on #687).
        throw e
    } catch (e: Exception) {
        AppLog.w(TAG, "Could not load the driver's trips from $offset", e)
        Result.failure(mapHttpError(e))
    }
}

// OffsetDateTime reads Go's times as they come, with an offset and up to nine fractional digits.
private fun MyTripDto.toTrip() = MyTrip(
    id = id,
    vehicleId = vehicleId,
    routeId = routeId,
    route = route?.let { MyTripRoute(it.shortName, it.longName, it.color, it.textColor) },
    headsign = headsign,
    start = OffsetDateTime.parse(startTime).toInstant(),
    end = endTime?.let { OffsetDateTime.parse(it).toInstant() },
)
