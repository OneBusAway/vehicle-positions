package org.onebusaway.vehiclepositions.data.local

import androidx.room.Entity
import androidx.room.PrimaryKey

@Entity(tableName = "vehicles")
data class VehicleEntity(
    @PrimaryKey
    val id: String,
    val isFavorite: Boolean = false,
    val lastUsedAt: Long = System.currentTimeMillis()
)