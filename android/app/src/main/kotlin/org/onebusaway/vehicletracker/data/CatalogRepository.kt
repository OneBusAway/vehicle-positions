package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.RouteDto
import org.onebusaway.vehicletracker.data.api.RouteTripsDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import retrofit2.HttpException
import javax.inject.Inject

/**
 * The schedule the driver picks from: the agency's routes, and one route's runs today.
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
}

private const val HTTP_NOT_FOUND = 404
private const val HTTP_SERVICE_UNAVAILABLE = 503

private fun mapCatalogError(e: Exception): ApiError = when {
    e is HttpException && (e.code() == HTTP_NOT_FOUND || e.code() == HTTP_SERVICE_UNAVAILABLE) ->
        ApiError.CatalogUnavailable
    else -> mapHttpError(e)
}
