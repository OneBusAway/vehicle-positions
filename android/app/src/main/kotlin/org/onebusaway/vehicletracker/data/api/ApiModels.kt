package org.onebusaway.vehicletracker.data.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable data class LoginRequest(val email: String, val password: String)

@Serializable data class LoginResponse(val token: String)

@Serializable data class VehicleDto(val id: String, val label: String)

@Serializable data class StartTripRequest(
    @SerialName("vehicle_id") val vehicleId: String,
    @SerialName("route_id") val routeId: String,
    @SerialName("gtfs_trip_id") val gtfsTripId: String,
)

@Serializable data class TripDto(
    val id: Long,
    @SerialName("vehicle_id") val vehicleId: String,
    @SerialName("route_id") val routeId: String,
    @SerialName("gtfs_trip_id") val gtfsTripId: String,
)

@Serializable data class EndTripRequest(@SerialName("trip_id") val tripId: Long)

@Serializable data class LocationReportDto(
    @SerialName("vehicle_id") val vehicleId: String,
    /** GTFS trip_id; null (omitted on the wire) when the driver only knows the route. */
    @SerialName("trip_id") val tripId: String? = null,
    @SerialName("route_id") val routeId: String,
    @SerialName("start_date") val startDate: String,
    val latitude: Double,
    val longitude: Double,
    val bearing: Double? = null,
    val speed: Double? = null,
    val accuracy: Double? = null,
    val timestamp: Long,
)

/** One route from the schedule catalog (`GET /api/v1/gtfs/routes`). */
@Serializable data class RouteDto(
    val id: String,
    @SerialName("short_name") val shortName: String,
    @SerialName("long_name") val longName: String,
    /** GTFS hex colour without a leading `#`, or "" when the feed omits it. */
    val color: String,
    @SerialName("text_color") val textColor: String,
    val type: Int,
)

@Serializable data class RoutesResponse(val routes: List<RouteDto>)

/**
 * One run of a route on a service date. [startsAt] and [endsAt] stay as they arrived — RFC 3339
 * with a zone offset — because only the screen that displays them knows which zone to read them
 * in; see `clockTime` in `ui/runs/HighlightedRun.kt`.
 */
@Serializable data class TripSummaryDto(
    val id: String,
    val headsign: String,
    @SerialName("direction_id") val directionId: Int? = null,
    @SerialName("starts_at") val startsAt: String,
    @SerialName("ends_at") val endsAt: String,
    @SerialName("first_stop") val firstStop: String,
    @SerialName("last_stop") val lastStop: String,
)

@Serializable data class RouteTripsDto(
    @SerialName("route_id") val routeId: String,
    @SerialName("service_date") val serviceDate: String,
    val timezone: String,
    val trips: List<TripSummaryDto>,
)
