package org.onebusaway.vehicletracker.data

import android.content.Context
import android.content.SharedPreferences
import androidx.core.content.edit

/** Light, dark or as the phone is, chosen in Settings. [storedAs] is its name on the phone. */
enum class ThemeChoice(val storedAs: String) {
    SYSTEM("system"),
    LIGHT("light"),
    DARK("dark"),
}

/** The theme chosen on this phone. It is the phone's, not the driver's, so it stays through Sign out. */
interface ThemeStore {
    var choice: ThemeChoice
}

/** Shared preferences rather than a DataStore: the theme is read before the first frame, and only they can be read at once. */
class PreferencesThemeStore(private val preferences: SharedPreferences) : ThemeStore {
    override var choice: ThemeChoice
        get() {
            val stored = preferences.getString(KEY, null)
            return ThemeChoice.entries.find { it.storedAs == stored } ?: ThemeChoice.SYSTEM
        }
        set(value) = preferences.edit { putString(KEY, value.storedAs) }

    private companion object {
        const val KEY = "theme"
    }
}

internal val Context.themePreferences: SharedPreferences
    get() = getSharedPreferences("theme", Context.MODE_PRIVATE)
