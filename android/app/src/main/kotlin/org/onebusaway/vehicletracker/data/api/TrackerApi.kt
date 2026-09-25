package org.onebusaway.vehicletracker.data.api

import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path

interface TrackerApi {
    @POST("api/v1/auth/login") suspend fun login(@Body body: LoginRequest): LoginResponse
    @GET("api/v1/vehicles") suspend fun myVehicles(): List<VehicleDto>
    @POST("api/v1/trips/start") suspend fun startTrip(@Body body: StartTripRequest): TripDto
    @POST("api/v1/trips/end") suspend fun endTrip(@Body body: EndTripRequest)
    @POST("api/v1/locations") suspend fun postLocation(@Body body: LocationReportDto)
    @GET("api/v1/gtfs/routes") suspend fun routes(): RoutesResponse

    // A GTFS route_id is arbitrary text, so the id is left to Retrofit to percent-encode
    // rather than interpolated raw: "1/A" must stay one path segment.
    @GET("api/v1/gtfs/routes/{route_id}/trips")
    suspend fun routeTrips(@Path("route_id") routeId: String): RouteTripsDto

    // A GTFS trip_id is arbitrary text too, so it is Retrofit's to percent-encode as well.
    @GET("api/v1/gtfs/trips/{trip_id}")
    suspend fun trip(@Path("trip_id") tripId: String): TripGeometryDto
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
