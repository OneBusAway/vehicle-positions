package org.onebusaway.vehiclepositions.util

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import javax.inject.Inject
import javax.inject.Singleton

val Context.dataStore by preferencesDataStore(name = "shift_prefs")

@Singleton
class ShiftStateManager @Inject constructor(
    @dagger.hilt.android.qualifiers.ApplicationContext private val context: Context
) {
    companion object {
        val KEY_ACTIVE_VEHICLE = stringPreferencesKey("active_vehicle_id")
        val KEY_SHIFT_ACTIVE = booleanPreferencesKey("shift_active")
        val KEY_SHIFT_START = longPreferencesKey("shift_start_time")
    }

    val activeVehicleId: Flow<String?> = context.dataStore.data
        .map { it[KEY_ACTIVE_VEHICLE] }

    val isShiftActive: Flow<Boolean> = context.dataStore.data
        .map { it[KEY_SHIFT_ACTIVE] ?: false }

    suspend fun startShift(vehicleId: String) {
        context.dataStore.edit {
            it[KEY_ACTIVE_VEHICLE] = vehicleId
            it[KEY_SHIFT_ACTIVE] = true
            it[KEY_SHIFT_START] = System.currentTimeMillis()
        }
    }

    suspend fun endShift() {
        context.dataStore.edit {
            it.remove(KEY_ACTIVE_VEHICLE)
            it[KEY_SHIFT_ACTIVE] = false
            it.remove(KEY_SHIFT_START)
        }
    }

    val shiftStartTime: Flow<Long?> = context.dataStore.data
        .map { it[KEY_SHIFT_START] }
}