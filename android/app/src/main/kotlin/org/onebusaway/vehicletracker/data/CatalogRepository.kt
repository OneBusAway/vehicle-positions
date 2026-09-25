package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.RouteDto
import org.onebusaway.vehicletracker.data.api.RouteTripsDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.api.TripGeometryDto
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import retrofit2.HttpException
import java.time.OffsetDateTime
import java.time.ZoneId
import javax.inject.Inject

/**
 * The schedule the driver picks from: the agency's routes, one route's runs today, and the shape
 * and stop times of the run they pick.
 *
 * The server registers these endpoints only when `GTFS_STATIC_URL` is set, so a server without a
 * schedule answers the routes call with `net/http`'s own 404 — a `text/plain` "404 page not
 * found", not JSON. That body is never read; the status alone is what the app acts on.
 */
class CatalogRepository @Inject constructor(
    private val apiProvider: TrackerApiProvider,
) {
    // apiProvider.get() is called here (not injected as a resolved TrackerApi) so that a missing
    // server URL (e.g. cold start racing session restore) surfaces as Result.failure instead of
    // an uncaught exception during construction.
    suspend fun routes(): Result<List<RouteDto>> = try {
        Result.success(apiProvider.get().routes().routes)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapCatalogError(e))
    }

    /**
     * A 404 here means "unknown route", which is a real error rather than a missing schedule: the
     * routes call has already succeeded by the time a driver can ask for one route's runs.
     */
    suspend fun trips(routeId: String): Result<RouteTripsDto> = try {
        Result.success(apiProvider.get().routeTrips(routeId))
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapHttpError(e))
    }

    /**
     * The run a driver picked, fetched before a start tells the server anything. As for [trips],
     * a 404 is a real error — an unknown trip — rather than a missing schedule.
     */
    suspend fun trip(tripId: String): Result<TripGeometry> = try {
        Result.success(apiProvider.get().trip(tripId).toTripGeometry())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(mapTripError(e))
    }
}

private const val HTTP_NOT_FOUND = 404
private const val HTTP_UNPROCESSABLE_ENTITY = 422
private const val HTTP_SERVICE_UNAVAILABLE = 503

private fun mapCatalogError(e: Exception): ApiError = when {
    e is HttpException && (e.code() == HTTP_NOT_FOUND || e.code() == HTTP_SERVICE_UNAVAILABLE) ->
        ApiError.CatalogUnavailable
    else -> mapHttpError(e)
}

private fun mapTripError(e: Exception): ApiError = when {
    e is HttpException && e.code() == HTTP_NOT_FOUND -> ApiError.Other("unknown trip")
    e is HttpException && e.code() == HTTP_UNPROCESSABLE_ENTITY -> ApiError.TripNotActiveToday
    e is HttpException && e.code() == HTTP_SERVICE_UNAVAILABLE -> ApiError.CatalogUnavailable
    else -> mapHttpError(e)
}

/**
 * Reads a run off the wire. A malformed shape pair is dropped rather than failing the run, as on
 * iOS, so it can never shift the coordinates after it. Times are read as an [OffsetDateTime] for
 * the reason `toRunPage` gives. Throws when the server names a zone or a time this platform
 * cannot read, which [CatalogRepository.trip] turns into a failed fetch.
 */
private fun TripGeometryDto.toTripGeometry() = TripGeometry(
    tripId = id,
    serviceDate = serviceDate,
    timezone = ZoneId.of(timezone),
    shapePoints = shape.points.filter { it.size == 2 }.map { (lat, lon) -> GeoPoint(lat, lon) },
    stops = stops.map { stop ->
        TripStop(
            id = stop.id,
            name = stop.name,
            lat = stop.lat,
            lon = stop.lon,
            alongShapeM = stop.alongShapeM,
            arrivalAt = OffsetDateTime.parse(stop.arrivalAt).toInstant(),
            departureAt = OffsetDateTime.parse(stop.departureAt).toInstant(),
        )
    },
    thresholds = AdherenceThresholds(
        maxShapeDistanceM = thresholds.maxShapeDistanceM,
        scheduleEarlyS = thresholds.scheduleEarlyS,
        scheduleLateS = thresholds.scheduleLateS,
    ),
)
