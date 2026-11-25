package org.onebusaway.vehicletracker

import android.app.Application
import android.content.res.Configuration
import android.os.LocaleList
import dagger.hilt.android.HiltAndroidApp
import org.onebusaway.vehicletracker.service.renameTripChannels
import org.onebusaway.vehicletracker.ui.theme.themeController

@HiltAndroidApp
class TrackerApp : Application() {
    private var locales: LocaleList? = null

    override fun onCreate() {
        super.onCreate()
        themeController(this).start()
        locales = resources.configuration.locales
        // The app's language may have changed while it was not running, as a language chosen for the app alone in
        // Settings on Android 13 and up does.
        renameTripChannels(this)
    }

    // A new language for the app, from the phone's settings or the app's own, while it runs.
    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        if (newConfig.locales == locales) return
        locales = newConfig.locales
        renameTripChannels(this)
    }
}
