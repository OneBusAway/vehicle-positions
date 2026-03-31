package org.onebusaway.vehiclepositions.data.remote

import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.Header
import retrofit2.http.POST

interface ApiService {

    @POST("api/v1/locations")
    suspend fun postLocation(
        @Header("Authorization") token: String,
        @Body request: LocationRequest
    ): Response<Unit>

    @POST("api/v1/auth/refresh")
    suspend fun refreshToken(
        @Body request: RefreshTokenRequest
    ): Response<RefreshTokenResponse>
}