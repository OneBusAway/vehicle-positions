package org.onebusaway.vehicletracker.service

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Names the trip's notification channels again when the phone's language changes. Android sends this to apps that
 * are not running too, so the app's notification settings say them in the new language straight away. A language
 * chosen for the app alone, on Android 13 and up, comes as a configuration change instead, which TrackerApp handles.
 */
class LanguageChangeReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_LOCALE_CHANGED) renameTripChannels(context)
    }
}
