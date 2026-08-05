package org.onebusaway.vehicletracker.data.api

import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST

interface TrackerApi {
    @POST("api/v1/auth/login") suspend fun login(@Body body: LoginRequest): LoginResponse
    @GET("api/v1/vehicles") suspend fun myVehicles(): List<VehicleDto>
    @POST("api/v1/trips/start") suspend fun startTrip(@Body body: StartTripRequest): TripDto
    @POST("api/v1/trips/end") suspend fun endTrip(@Body body: EndTripRequest)
    @POST("api/v1/locations") suspend fun postLocation(@Body body: LocationReportDto)
}

/**
 * A lazy accessor for [TrackerApi], injected instead of a resolved [TrackerApi] wherever
 * resolution can throw (e.g. [org.onebusaway.vehicletracker.di.ApiHolder.api] when the server
 * URL is absent) and the caller wants that exception to surface inside its own try/catch rather
 * than at Hilt-graph-construction time.
 */
fun interface TrackerApiProvider {
    fun get(): TrackerApi
}
