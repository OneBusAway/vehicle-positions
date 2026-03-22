package org.onebusaway.vehiclepositions.data.local

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import kotlinx.coroutines.flow.Flow

@Dao
interface VehicleDao {

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insert(vehicle: VehicleEntity)

    // Favorites are pinned at top, sorted by most recently used
    @Query("SELECT * FROM vehicles WHERE isFavorite = 1 ORDER BY lastUsedAt DESC")
    fun getFavorites(): Flow<List<VehicleEntity>>

    // Cap recents at 10 — no need to show stale shifts from weeks ago
    @Query("SELECT * FROM vehicles ORDER BY lastUsedAt DESC LIMIT 10")
    fun getRecents(): Flow<List<VehicleEntity>>

    @Query("UPDATE vehicles SET isFavorite = :isFavorite WHERE id = :vehicleId")
    suspend fun setFavorite(vehicleId: String, isFavorite: Boolean)

    // Called when a shift ends to keep lastUsedAt accurate
    @Query("UPDATE vehicles SET lastUsedAt = :timestamp WHERE id = :vehicleId")
    suspend fun updateLastUsed(vehicleId: String, timestamp: Long)

    @Query("SELECT * FROM vehicles WHERE id = :vehicleId")
    suspend fun getVehicleById(vehicleId: String): VehicleEntity?

    // Favorites float to the top, then sorted by recency within each group
    @Query("SELECT * FROM vehicles WHERE id LIKE '%' || :query || '%' ORDER BY isFavorite DESC, lastUsedAt DESC")
    fun searchVehicles(query: String): Flow<List<VehicleEntity>>
}