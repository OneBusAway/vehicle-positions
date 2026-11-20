package org.onebusaway.vehicletracker.ui.language

import android.app.LocaleManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.res.Resources
import android.os.Build
import android.os.LocaleList
import android.provider.Settings
import androidx.annotation.RequiresApi
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.AppLog
import org.xmlpull.v1.XmlPullParser
import java.util.Locale

private const val TAG = "AppLanguages"
private const val ANDROID_NAMESPACE = "http://schemas.android.com/apk/res/android"

/** The language of values/, which res/resources.properties declares: what a phone in any other language is shown. */
private val UNQUALIFIED = Locale.ENGLISH

/**
 * One of the app's languages as the Language list names it: [ownName] in its own language and script, so a driver
 * finds theirs without reading English, and [englishName] under it, or null when the two are the same.
 */
internal data class AppLanguage(val tag: String, val ownName: String, val englishName: String?)

internal fun appLanguage(locale: Locale): AppLanguage {
    // Some languages write their own name in lower case ("español"); on a line of its own it starts in upper case.
    val own = locale.getDisplayName(locale).replaceFirstChar { it.titlecase(locale) }
    val english = locale.getDisplayName(Locale.ENGLISH)
    return AppLanguage(locale.toLanguageTag(), own, english.takeIf { it != own })
}

/**
 * The language the app's strings are in, of [available], for a phone or app set to [preferred] in that order: the
 * first of them the app has, as Android picks strings, and otherwise English. A language the app has for one region is
 * taken for another region of it before the next language in [preferred], but never across scripts.
 */
internal fun shownLanguage(preferred: List<Locale>, available: List<Locale>): Locale {
    for (wanted in preferred) {
        val sameLanguage = available.filter { it.language == wanted.language && it.script == wanted.script }
        val match = sameLanguage.firstOrNull { it == wanted }
            ?: sameLanguage.firstOrNull { it.country.isEmpty() }
            ?: sameLanguage.firstOrNull()
        if (match != null) return match
    }
    return UNQUALIFIED
}

/** The Language list: the language [shown] first, as Settings lists the one in use, then the rest by English name. */
internal fun languageChoices(available: List<Locale>, shown: Locale): List<AppLanguage> {
    val (current, others) = available.map(::appLanguage).partition { it.tag == shown.toLanguageTag() }
    return current + others.sortedBy { it.englishName ?: it.ownName }
}

/**
 * The app's languages, from the locale config the build writes from its values-<language> folders (#505): a language
 * added there is listed here with no change to this code. Read from the resource rather than with LocaleConfig, which
 * comes with Android 13, so the row names the language shown on every version.
 */
internal fun appLanguageTags(resources: Resources): List<String> =
    resources.getXml(R.xml._generated_res_locale_config).use { parser ->
        buildList {
            while (parser.next() != XmlPullParser.END_DOCUMENT) {
                if (parser.eventType == XmlPullParser.START_TAG && parser.name == "locale") {
                    parser.getAttributeValue(ANDROID_NAMESPACE, "name")?.let(::add)
                }
            }
        }
    }

/** What the Language row does to the phone: through Android in the app, and through the phone's settings in a test. */
internal interface LanguageSettings {
    /** Shows the app alone in [tag], on Android 13 and up. */
    fun setAppLanguage(tag: String)

    fun openPhoneLanguageSettings()
}

/**
 * What a tap on the Language row does on Android [sdk]. From Android 13 the app can be set to a language of its own,
 * so the row opens the app's list. Below 13 only AppCompat can do that, which the app does not use, so the row opens
 * the phone's language settings and says so.
 */
internal class LanguageRowController(private val sdk: Int, private val settings: LanguageSettings) {
    val opensPhoneSettings: Boolean get() = sdk < Build.VERSION_CODES.TIRAMISU

    /** True when the app's own list should open. */
    fun tap(): Boolean {
        if (opensPhoneSettings) settings.openPhoneLanguageSettings()
        return !opensPhoneSettings
    }

    /**
     * Sets the app to [tag]. Android then gives the app the new language as a change of configuration, on which the
     * screens are drawn again in it, the trip's notification channels are renamed and the trip's notification is
     * posted again (TrackerApp and LocationTrackingService). The language [shown] already is left as it is, so a phone
     * whose language the app follows keeps following it.
     */
    fun choose(tag: String, shown: String) {
        if (opensPhoneSettings || tag == shown) return
        settings.setAppLanguage(tag)
    }
}

internal class AndroidLanguageSettings(private val context: Context) : LanguageSettings {
    @RequiresApi(Build.VERSION_CODES.TIRAMISU)
    override fun setAppLanguage(tag: String) {
        context.getSystemService(LocaleManager::class.java).applicationLocales = LocaleList.forLanguageTags(tag)
    }

    override fun openPhoneLanguageSettings() {
        try {
            context.startActivity(Intent(Settings.ACTION_LOCALE_SETTINGS))
        } catch (e: ActivityNotFoundException) {
            // A phone whose maker took the screen out. The driver can still find it in Settings.
            AppLog.w(TAG, "No language settings screen to open", e)
        }
    }
}
