package org.onebusaway.vehicletracker.data

import retrofit2.HttpException
import java.io.IOException

sealed class ApiError : Exception() {
    object NotAssigned : ApiError()
    object TripAlreadyActive : ApiError()
    object Unauthorized : ApiError()

    /**
     * The server has no schedule to offer: either `GTFS_STATIC_URL` is unset, so the catalog
     * routes were never registered, or its index is still loading. Only [CatalogRepository]
     * knows which endpoint answered — and so whether a 404 means this or "unknown route" — so it
     * is the only place that produces this; [mapHttpError] has no endpoint to reason about.
     */
    object CatalogUnavailable : ApiError()

    /**
     * `422 trip not active on date`: the run is not in service on the day the server resolves it
     * to, which once the service day has rolled over at 03:00 is no longer the day the run list
     * was loaded for. Reloading the list is what helps.
     */
    object TripNotActiveToday : ApiError()

    data class Other(val msg: String) : ApiError()
}

fun mapHttpError(e: Exception): ApiError = when {
    e is HttpException && e.code() == 401 -> ApiError.Unauthorized
    e is HttpException && e.code() == 403 -> ApiError.NotAssigned
    e is HttpException && e.code() == 409 -> ApiError.TripAlreadyActive
    e is IOException -> ApiError.Other("network")
    else -> ApiError.Other(e.message ?: "unknown")
}
