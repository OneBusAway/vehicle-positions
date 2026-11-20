package org.onebusaway.vehicletracker.service

import android.app.LocaleManager
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.res.Configuration
import android.os.Build
import android.os.LocaleList
import android.os.SystemClock
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.language.AndroidLanguageSettings
import org.onebusaway.vehicletracker.ui.language.LanguageRowController

/**
 * The trip's notification channels are listed by name in the app's notification settings, so they are named again
 * when the app's language changes. en-XA, a debug build's accented English, stands in for another language.
 */
@RunWith(AndroidJUnit4::class)
class TripChannelLanguageTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val manager = context.getSystemService(NotificationManager::class.java)
    private val ids = listOf("tracking", "tracking_attention")

    private fun names(): List<String?> = ids.map { manager.getNotificationChannel(it)?.name?.toString() }

    private fun namesIn(language: Context): List<String?> = listOf(
        language.getString(R.string.tracking_notification_channel_name),
        language.getString(R.string.tracking_notification_attention_channel_name),
    )

    private fun inLanguage(tag: String): Context =
        context.createConfigurationContext(Configuration(context.resources.configuration).apply { setLocales(LocaleList.forLanguageTags(tag)) })

    /** The channels' names once Android has handled the change, which it does off this thread. */
    private fun namesOnceThey(expected: List<String?>): List<String?> {
        val deadline = SystemClock.uptimeMillis() + 10_000
        var names = names()
        while (names != expected && SystemClock.uptimeMillis() < deadline) {
            SystemClock.sleep(50)
            names = names()
        }
        return names
    }

    @Test
    fun aNewPhoneLanguageRenamesTheChannels() {
        TrackingNotification(context)
        val accented = inLanguage("en-XA")
        assertNotEquals("this debug build has en-XA strings", namesIn(context), namesIn(accented))

        // What Android hands the receiver after the phone's language changed.
        LanguageChangeReceiver().onReceive(accented, Intent(Intent.ACTION_LOCALE_CHANGED))
        assertEquals(namesIn(accented), names())

        LanguageChangeReceiver().onReceive(context, Intent(Intent.ACTION_LOCALE_CHANGED))
        assertEquals(namesIn(context), names())
    }

    @Test
    fun anythingButALanguageChangeLeavesTheNamesAlone() {
        TrackingNotification(context)

        LanguageChangeReceiver().onReceive(inLanguage("en-XA"), Intent("org.onebusaway.vehicletracker.action.NOT_A_LANGUAGE"))

        assertEquals(namesIn(context), names())
    }

    @Test
    fun aLanguageChangeMakesNoChannelsForADriverWhoHasNone() {
        ids.forEach(manager::deleteNotificationChannel)

        LanguageChangeReceiver().onReceive(context, Intent(Intent.ACTION_LOCALE_CHANGED))

        assertEquals(listOf(null, null), names())
    }

    @Test
    fun aLanguageChosenForTheAppAloneRenamesTheChannels() {
        assumeTrue("a language for one app comes with Android 13", Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)
        TrackingNotification(context)
        val english = namesIn(context)
        val accented = namesIn(inLanguage("en-XA"))
        assertNotEquals("this debug build has en-XA strings", english, accented)
        val locales = context.getSystemService(LocaleManager::class.java)

        try {
            locales.applicationLocales = LocaleList.forLanguageTags("en-XA")

            assertEquals(accented, namesOnceThey(accented))
        } finally {
            // Back to the phone's language, and the channels with it, before any other test reads a string.
            locales.applicationLocales = LocaleList.getEmptyLocaleList()
            assertEquals(english, namesOnceThey(english))
        }
    }

    @Test
    fun aLanguageChosenOnTheLanguageRowRenamesTheChannels() {
        assumeTrue("the app's own list comes with Android 13", Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)
        TrackingNotification(context)
        val english = namesIn(context)
        val accented = namesIn(inLanguage("en-XA"))
        assertNotEquals("this debug build has en-XA strings", english, accented)
        val row = LanguageRowController(Build.VERSION.SDK_INT, AndroidLanguageSettings(context))

        try {
            row.choose("en-XA", shown = "en")

            assertEquals(accented, namesOnceThey(accented))
        } finally {
            context.getSystemService(LocaleManager::class.java).applicationLocales = LocaleList.getEmptyLocaleList()
            assertEquals(english, namesOnceThey(english))
        }
    }
}
