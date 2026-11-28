package org.onebusaway.vehicletracker.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.retryWhen
import java.io.IOException
import kotlin.time.Duration.Companion.seconds

private const val TAG = "TripSettingsStore"

/** How long after a failed read the trip settings are read again. */
internal val READ_RETRY_DELAY = 5.seconds

/** When the display stays on while Tracking shows. [storedAs] is its name on the phone. */
enum class ScreenOn(val storedAs: String) {
    CHARGING("charging"),
    ALWAYS("always"),
}

/** What is up on Tracking's map: the way the vehicle goes, or north. [storedAs] is its name on the phone. */
enum class MapUp(val storedAs: String) {
    HEADING("heading"),
    NORTH("north"),
}

/** What the driver chose for Tracking. The defaults are what it does when nothing was chosen, or nothing can be read. */
data class TripSettings(
    val screenOn: ScreenOn = ScreenOn.CHARGING,
    val mapUp: MapUp = MapUp.HEADING,
    /** The phone vibrates when reporting has a problem. */
    val vibrates: Boolean = true,
)

/** [TripSettings] of this phone. They are the phone's, not the driver's, so they stay through Sign out. */
interface TripSettingsStore {
    val settings: Flow<TripSettings>

    /** Each throws [IOException] when the phone can't save the choice. */
    suspend fun setScreenOn(screenOn: ScreenOn)
    suspend fun setMapUp(mapUp: MapUp)
    suspend fun setVibrates(vibrates: Boolean)
}

/** Internal, not private, only because [org.onebusaway.vehicletracker.di.AppModule] provides the store. */
internal val Context.tripSettingsDataStore: DataStore<Preferences> by preferencesDataStore(name = "trip_settings")

class DataStoreTripSettingsStore(private val dataStore: DataStore<Preferences>) : TripSettingsStore {
    private object Keys {
        val SCREEN_ON = stringPreferencesKey("screen_on")
        val MAP_UP = stringPreferencesKey("map_up")
        val VIBRATES = booleanPreferencesKey("vibrates")
    }

    override val settings: Flow<TripSettings> = dataStore.data
        // Choices that can't be read are the defaults, as for the session: Tracking collects this, and a read failure let
        // through would crash it. They are read again a little later, so the choices come back once the phone can read
        // them. Anything that is not an IO failure still propagates.
        .retryWhen { e, attempt ->
            if (e !is IOException) return@retryWhen false
            if (attempt == 0L) AppLog.w(TAG, "Could not read the trip settings", e)
            emit(emptyPreferences())
            delay(READ_RETRY_DELAY)
            true
        }
        .map { prefs ->
            val none = TripSettings()
            TripSettings(
                screenOn = ScreenOn.entries.find { it.storedAs == prefs[Keys.SCREEN_ON] } ?: none.screenOn,
                mapUp = MapUp.entries.find { it.storedAs == prefs[Keys.MAP_UP] } ?: none.mapUp,
                vibrates = prefs[Keys.VIBRATES] ?: none.vibrates,
            )
        }
        // A read that keeps failing gives the defaults each time.
        .distinctUntilChanged()

    override suspend fun setScreenOn(screenOn: ScreenOn) {
        dataStore.edit { it[Keys.SCREEN_ON] = screenOn.storedAs }
    }

    override suspend fun setMapUp(mapUp: MapUp) {
        dataStore.edit { it[Keys.MAP_UP] = mapUp.storedAs }
    }

    override suspend fun setVibrates(vibrates: Boolean) {
        dataStore.edit { it[Keys.VIBRATES] = vibrates }
    }
}
