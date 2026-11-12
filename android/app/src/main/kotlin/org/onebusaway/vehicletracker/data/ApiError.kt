package org.onebusaway.vehicletracker.data

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import retrofit2.HttpException
import java.io.IOException

sealed class ApiError : Exception() {
    object NotAssigned : ApiError()
    object TripAlreadyActive : ApiError()

    /** `409 vehicle already has an active trip`: another driver is running a trip on the vehicle. */
    object VehicleBusy : ApiError()

    /** `409 vehicle is inactive`: dispatch took the vehicle out of service after the list was loaded. */
    object VehicleInactive : ApiError()
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

    /**
     * The end of a trip ended on this phone only could not be sent before the next start. The
     * server still has that trip open and would refuse the start, so the start is not sent, and
     * the driver is told it is the previous trip that is stuck rather than this one.
     */
    object PreviousTripNotEnded : ApiError()

    /**
     * Something answered the sign-in, but not as a tracker server does: a 404, a 405, or a reply
     * that isn't our JSON. Those mean something else on every other call, so only
     * [AuthRepository.login] produces this.
     */
    object NotATrackerServer : ApiError()

    /** `429 too many attempts`, the server's limit on sign-ins. Only [AuthRepository.login] produces this. */
    object TooManyAttempts : ApiError()

    /**
     * The phone's network security policy refused a plain `http://` address. OkHttp throws an
     * [IOException] for it, which [mapHttpError] reads as no network, so only
     * [AuthRepository.login] produces this.
     */
    object CleartextBlocked : ApiError()

    data class Other(val msg: String) : ApiError()
}

fun mapHttpError(e: Exception): ApiError = when {
    e is HttpException && e.code() == 401 -> ApiError.Unauthorized
    e is HttpException && e.code() == 403 -> ApiError.NotAssigned
    e is HttpException && e.code() == 409 -> ApiError.TripAlreadyActive
    e is IOException -> ApiError.Other("network")
    else -> ApiError.Other(e.message ?: "unknown")
}

/**
 * [mapHttpError] for `POST /api/v1/trips/start`, where a 409 has three causes the server tells apart only by its
 * `error` text: this driver's own open trip, another driver's trip on the vehicle, or a vehicle taken out of service.
 * Anything it doesn't recognise stays [ApiError.TripAlreadyActive], what every 409 there meant before.
 */
fun mapStartTripError(e: Exception): ApiError {
    if (e !is HttpException || e.code() != 409) return mapHttpError(e)
    val message = runCatching {
        e.response()?.errorBody()?.string()?.let { Json.parseToJsonElement(it).jsonObject["error"]?.jsonPrimitive?.content }
    }.getOrNull()
    return when (message) {
        "vehicle already has an active trip" -> ApiError.VehicleBusy
        "vehicle is inactive" -> ApiError.VehicleInactive
        else -> ApiError.TripAlreadyActive
    }
}
