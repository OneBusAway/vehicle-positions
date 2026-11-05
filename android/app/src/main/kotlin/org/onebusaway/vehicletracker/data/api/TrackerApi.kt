package org.onebusaway.vehicletracker.data.api

import retrofit2.Call
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
    @POST("api/v1/locations/history") suspend fun postLocationHistory(@Body body: LocationHistoryDto)
    @GET("api/v1/gtfs/routes") suspend fun routes(): RoutesResponse

    // A GTFS route_id is arbitrary text, so the id is left to Retrofit to percent-encode
    // rather than interpolated raw: "1/A" must stay one path segment.
    @GET("api/v1/gtfs/routes/{route_id}/trips")
    suspend fun routeTrips(@Path("route_id") routeId: String): RouteTripsDto

    // A GTFS trip_id is arbitrary text too, so it is Retrofit's to percent-encode as well.
    @GET("api/v1/gtfs/trips/{trip_id}")
    suspend fun trip(@Path("trip_id") tripId: String): TripGeometryDto

    @GET("api/v1/map") suspend fun mapConfig(): MapConfigDto
}

/**
 * `POST /api/v1/auth/refresh`, for [TokenRefresher] only. It blocks, because OkHttp calls the
 * refresher on its own threads, and returns the whole [retrofit2.Response], so a refused refresh
 * token (401) can be told from a server that can't refresh right now (5xx, 429) without exceptions.
 */
interface RefreshApi {
    @POST("api/v1/auth/refresh") fun refresh(@Body body: RefreshRequest): Call<LoginResponse>
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
