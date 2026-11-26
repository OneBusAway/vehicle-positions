package org.onebusaway.vehicletracker.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.map
import java.io.IOException

private const val TAG = "LocationNoticeStore"

/**
 * Whether this phone has agreed to the location notice, which Google Play wants before Android is asked for location.
 * The phone's, not a driver's: it is kept through Sign out, and shown once for every driver on it.
 */
interface LocationNoticeStore {
    /** False until agreed, and while the phone can't read its record: then the notice shows again. */
    val agreed: Flow<Boolean>

    /** Throws [IOException] when the phone can't save it. */
    suspend fun agree()
}

/** Internal, not private, only because [org.onebusaway.vehicletracker.di.AppModule] provides the store. */
internal val Context.locationNoticeDataStore: DataStore<Preferences> by preferencesDataStore(name = "location_notice")

class DataStoreLocationNoticeStore(private val dataStore: DataStore<Preferences>) : LocationNoticeStore {
    private val agreedKey = booleanPreferencesKey("agreed")

    override val agreed: Flow<Boolean> = dataStore.data
        // A screen collects this, and a read failure let through would crash it. Anything that is not an IO failure
        // still propagates.
        .catch { e ->
            if (e !is IOException) throw e
            AppLog.w(TAG, "Could not read whether the location notice was agreed to", e)
            emit(emptyPreferences())
        }
        .map { it[agreedKey] == true }

    override suspend fun agree() {
        dataStore.edit { it[agreedKey] = true }
    }
}
