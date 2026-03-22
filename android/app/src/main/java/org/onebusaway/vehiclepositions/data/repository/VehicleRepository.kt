package org.onebusaway.vehiclepositions.data.repository

import android.util.Log
import kotlinx.coroutines.flow.Flow
import org.onebusaway.vehiclepositions.data.local.VehicleDao
import org.onebusaway.vehiclepositions.data.local.VehicleEntity
import org.onebusaway.vehiclepositions.data.remote.ApiService
import org.onebusaway.vehiclepositions.data.remote.LocationRequest
import org.onebusaway.vehiclepositions.data.remote.RefreshTokenRequest
import org.onebusaway.vehiclepositions.util.TokenManager
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class VehicleRepository @Inject constructor(
    private val vehicleDao: VehicleDao,
    private val apiService: ApiService,
    private val tokenManager: TokenManager
) {
    companion object {
        private const val TAG = "VehicleRepository"
    }

    // ── Local DB ──────────────────────────────────────────────

    fun getFavorites(): Flow<List<VehicleEntity>> = vehicleDao.getFavorites()

    fun getRecents(): Flow<List<VehicleEntity>> = vehicleDao.getRecents()

    fun searchVehicles(query: String): Flow<List<VehicleEntity>> =
        vehicleDao.searchVehicles(query)

    suspend fun saveVehicleToRecents(vehicleId: String) {
        if (vehicleId.isBlank()) return
        val existing = vehicleDao.getVehicleById(vehicleId)
        if (existing != null) {
            vehicleDao.updateLastUsed(vehicleId, System.currentTimeMillis())
        } else {
            vehicleDao.insert(VehicleEntity(id = vehicleId))
        }
    }

    suspend fun toggleFavorite(vehicleId: String, isFavorite: Boolean) {
        vehicleDao.setFavorite(vehicleId, isFavorite)
    }

    // ── Remote API ────────────────────────────────────────────

    suspend fun postLocation(request: LocationRequest): Result<Unit> {
        return try {
            val token = tokenManager.getToken()

            if (token == null) {
                Log.w(TAG, "No JWT token — skipping location report")
                return Result.success(Unit)
            }

            val response = apiService.postLocation("Bearer $token", request)

            when {
                response.isSuccessful -> {
                    // Never log GPS coordinates — they're PII
                    Log.d(TAG, "Location reported ✅ vehicle=${request.vehicleId}")
                    Result.success(Unit)
                }
                response.code() == 401 -> {
                    Log.w(TAG, "401 Unauthorized — token may be expired")
                    Result.failure(Exception("401"))
                }
                response.code() == 429 -> {
                    // Back off gracefully — no need to surface this as a failure
                    Log.w(TAG, "429 Rate limited")
                    Result.success(Unit)
                }
                response.code() == 400 -> {
                    Log.e(TAG, "400 Bad request — invalid vehicle_id: ${request.vehicleId}")
                    Result.failure(Exception("400: invalid vehicle_id"))
                }
                else -> {
                    Log.e(TAG, "Server error ${response.code()} — will retry next cycle")
                    Result.failure(Exception("${response.code()}"))
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Network error posting location: ${e.message}")
            Result.failure(e)
        }
    }

    suspend fun refreshToken(): Result<Unit> {
        return try {
            val refreshToken = tokenManager.getRefreshToken()
                ?: return Result.failure(Exception("No refresh token"))
            val response = apiService.refreshToken(RefreshTokenRequest(refreshToken))
            tokenManager.saveToken(response.token)
            tokenManager.saveRefreshToken(response.refreshToken)
            Log.d(TAG, "Token refreshed successfully")
            Result.success(Unit)
        } catch (e: Exception) {
            Log.e(TAG, "Token refresh failed: ${e.message}")
            Result.failure(e)
        }
    }
}