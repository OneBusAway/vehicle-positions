package org.onebusaway.vehiclepositions.data.local

import androidx.room.Database
import androidx.room.RoomDatabase

@Database(
    entities = [VehicleEntity::class],
    version = 1,
    exportSchema = false
)
abstract class AppDatabase : RoomDatabase() {
    abstract fun vehicleDao(): VehicleDao
}