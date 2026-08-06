package org.onebusaway.vehicletracker.data

import retrofit2.HttpException
import java.io.IOException

sealed class ApiError : Exception() {
    object NotAssigned : ApiError()
    object TripAlreadyActive : ApiError()
    object Unauthorized : ApiError()
    data class Other(val msg: String) : ApiError()
}

fun mapHttpError(e: Exception): ApiError = when {
    e is HttpException && e.code() == 401 -> ApiError.Unauthorized
    e is HttpException && e.code() == 403 -> ApiError.NotAssigned
    e is HttpException && e.code() == 409 -> ApiError.TripAlreadyActive
    e is IOException -> ApiError.Other("network")
    else -> ApiError.Other(e.message ?: "unknown")
}
