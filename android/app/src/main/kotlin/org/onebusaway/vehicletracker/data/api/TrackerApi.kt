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
