package org.onebusaway.vehiclepositions.data.remote

import com.google.gson.annotations.SerializedName

data class LocationRequest(
    @SerializedName("vehicle_id") val vehicleId: String,
    @SerializedName("trip_id") val tripId: String? = null,
    @SerializedName("latitude") val latitude: Double,
    @SerializedName("longitude") val longitude: Double,
    @SerializedName("bearing") val bearing: Float? = null,
    @SerializedName("speed") val speed: Float? = null,
    @SerializedName("accuracy") val accuracy: Float? = null,
    @SerializedName("timestamp") val timestamp: Long
)

data class RefreshTokenRequest(
    @SerializedName("refresh_token") val refreshToken: String
)

data class RefreshTokenResponse(
    @SerializedName("access_token") val token: String,
    @SerializedName("refresh_token") val refreshToken: String
)