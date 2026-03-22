package org.onebusaway.vehiclepositions.data.remote

import com.google.gson.annotations.SerializedName

data class LocationRequest(
    @SerializedName("vehicle_id") val vehicleId: String,
    @SerializedName("latitude") val latitude: Double,
    @SerializedName("longitude") val longitude: Double,
    @SerializedName("bearing") val bearing: Float,
    @SerializedName("speed") val speed: Float,
    @SerializedName("accuracy") val accuracy: Float,
    @SerializedName("timestamp") val timestamp: Long
)

data class RefreshTokenRequest(
    @SerializedName("refresh_token") val refreshToken: String
)

data class RefreshTokenResponse(
    @SerializedName("token") val token: String,
    @SerializedName("refresh_token") val refreshToken: String
)