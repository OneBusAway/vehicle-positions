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

/**
 * One run as `GET /api/v1/gtfs/trips/{trip_id}` serves it: its shape, its stops with absolute
 * scheduled times, and the adherence thresholds the server applies. The times stay as they
 * arrived, for the reason [TripSummaryDto] gives; `CatalogRepository.trip` resolves them.
 */
@Serializable data class TripGeometryDto(
    val id: String,
    @SerialName("route_id") val routeId: String,
    val headsign: String,
    @SerialName("direction_id") val directionId: Int? = null,
    @SerialName("service_date") val serviceDate: String,
    val timezone: String,
    val route: TripRouteDto,
    val shape: ShapeDto,
    val stops: List<TripStopDto>,
    val thresholds: AdherenceThresholdsDto,
)

@Serializable data class TripRouteDto(
    @SerialName("short_name") val shortName: String,
    @SerialName("long_name") val longName: String,
    val color: String,
    @SerialName("text_color") val textColor: String,
)

@Serializable data class ShapeDto(
    @SerialName("length_m") val lengthM: Double,
    /** `[lat, lon]` pairs in shape order. */
    val points: List<List<Double>>,
)

@Serializable data class TripStopDto(
    val id: String,
    val name: String,
    val sequence: Int,
    val lat: Double,
    val lon: Double,
    @SerialName("along_shape_m") val alongShapeM: Double,
    @SerialName("arrival_at") val arrivalAt: String,
    @SerialName("departure_at") val departureAt: String,
)

@Serializable data class AdherenceThresholdsDto(
    @SerialName("max_shape_distance_m") val maxShapeDistanceM: Double,
    @SerialName("schedule_early_s") val scheduleEarlyS: Int,
    @SerialName("schedule_late_s") val scheduleLateS: Int,
)
