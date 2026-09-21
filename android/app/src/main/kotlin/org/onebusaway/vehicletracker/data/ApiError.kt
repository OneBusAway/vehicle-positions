package org.onebusaway.vehicletracker.data

import retrofit2.HttpException
import java.io.IOException

sealed class ApiError : Exception() {
    object NotAssigned : ApiError()
    object TripAlreadyActive : ApiError()
    object Unauthorized : ApiError()

    /**
     * The server has no schedule to offer: either `GTFS_STATIC_URL` is unset, so the catalog
     * routes were never registered, or its index is still loading. Only
     * [CatalogRepository.routes] can tell those from the 404 that means "unknown route", so it
     * is the only place that produces this; [mapHttpError] has no endpoint to reason about.
     */
    object CatalogUnavailable : ApiError()

    data class Other(val msg: String) : ApiError()
}

fun mapHttpError(e: Exception): ApiError = when {
    e is HttpException && e.code() == 401 -> ApiError.Unauthorized
    e is HttpException && e.code() == 403 -> ApiError.NotAssigned
    e is HttpException && e.code() == 409 -> ApiError.TripAlreadyActive
    e is IOException -> ApiError.Other("network")
    else -> ApiError.Other(e.message ?: "unknown")
}
