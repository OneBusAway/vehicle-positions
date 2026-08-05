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
    @SerialName("trip_id") val tripId: String,
    val latitude: Double,
    val longitude: Double,
    val bearing: Double? = null,
    val speed: Double? = null,
    val accuracy: Double? = null,
    val timestamp: Long,
)
