package org.onebusaway.vehicletracker.ui.theme

import android.app.UiModeManager
import android.content.Context
import android.content.res.Configuration
import android.os.Build
import androidx.annotation.RequiresApi
import org.onebusaway.vehicletracker.data.PreferencesThemeStore
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.data.ThemeStore
import org.onebusaway.vehicletracker.data.themePreferences

/** What gives the whole app its theme from Android 12: Android in the app, and a list in a test. */
fun interface AppNightMode {
    fun set(choice: ThemeChoice)
}

/**
 * Shows the app in the theme chosen in Settings, on Android [sdk]. From Android 12 the system gives it to the whole app,
 * the launch screen too, and the activity on screen changes in place. Below 12 each activity gives it to itself, as it is
 * made ([activityConfiguration]). Either way it is a night mode like the phone's own, so what asks for that, in code, in
 * resources or for the system bars' icons, has the app's.
 */
class ThemeController(private val sdk: Int, private val store: ThemeStore, private val app: AppNightMode) {
    private val systemSetsIt get() = sdk >= Build.VERSION_CODES.S

    val choice: ThemeChoice get() = store.choice

    /** As the process starts: a choice restored to a new phone is stored there, but Android has not been told of it. */
    fun start() {
        if (systemSetsIt) app.set(store.choice)
    }

    /** True when the activity on screen has to be made again to show [choice], as below Android 12. */
    fun choose(choice: ThemeChoice): Boolean {
        if (choice == store.choice) return false
        store.choice = choice
        if (systemSetsIt) app.set(choice)
        return !systemSetsIt
    }

    /** What an activity puts over its configuration as it is made. Null when there is nothing for it to do. */
    fun activityConfiguration(): Configuration? {
        val night = nightBits(store.choice)
        if (systemSetsIt || night == Configuration.UI_MODE_NIGHT_UNDEFINED) return null
        return nightConfiguration(night)
    }
}

internal fun themeController(context: Context) =
    ThemeController(Build.VERSION.SDK_INT, PreferencesThemeStore(context.themePreferences), AndroidAppNightMode(context))

/** A configuration's night bits for [choice]: undefined leaves the phone's. */
internal fun nightBits(choice: ThemeChoice): Int = when (choice) {
    ThemeChoice.SYSTEM -> Configuration.UI_MODE_NIGHT_UNDEFINED
    ThemeChoice.LIGHT -> Configuration.UI_MODE_NIGHT_NO
    ThemeChoice.DARK -> Configuration.UI_MODE_NIGHT_YES
}

/** Only the night bits: the rest of the configuration stays the phone's. */
internal fun nightConfiguration(night: Int) = Configuration().apply { uiMode = night }

private class AndroidAppNightMode(private val context: Context) : AppNightMode {
    @RequiresApi(Build.VERSION_CODES.S)
    override fun set(choice: ThemeChoice) {
        val mode = when (choice) {
            // Leaves it to the phone's own.
            ThemeChoice.SYSTEM -> UiModeManager.MODE_NIGHT_AUTO
            ThemeChoice.LIGHT -> UiModeManager.MODE_NIGHT_NO
            ThemeChoice.DARK -> UiModeManager.MODE_NIGHT_YES
        }
        context.getSystemService(UiModeManager::class.java).setApplicationNightMode(mode)
    }
}
