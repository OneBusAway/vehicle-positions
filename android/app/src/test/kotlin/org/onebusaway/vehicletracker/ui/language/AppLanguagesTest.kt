package org.onebusaway.vehicletracker.ui.language

import android.os.Build
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Locale

/**
 * The Language row on the first welcome screen and in Help: which language it names, how the list names each of the
 * app's languages, and what a tap does on each version of Android.
 */
class AppLanguagesTest {
    private fun locales(vararg tags: String) = tags.map(Locale::forLanguageTag)

    // --- The language the row names ---

    @Test fun `the row names English for a phone in English`() {
        assertEquals("en", shownLanguage(locales("en-US"), locales("en")).toLanguageTag())
    }

    @Test fun `a phone language the app lacks shows English, the app's own strings`() {
        assertEquals("en", shownLanguage(locales("fr-FR"), locales("en")).toLanguageTag())
        assertEquals("en", shownLanguage(locales("fr-FR"), locales("hi", "es")).toLanguageTag())
    }

    @Test fun `the phone's first language the app has is the one shown, as Android picks strings`() {
        assertEquals("hi", shownLanguage(locales("fr-FR", "hi-IN", "en-US"), locales("en", "hi")).toLanguageTag())
    }

    @Test fun `a language the app has for one region is shown for the same language elsewhere`() {
        assertEquals("pt-BR", shownLanguage(locales("pt-PT"), locales("en", "pt-BR")).toLanguageTag())
    }

    @Test fun `the language itself is preferred over another region of it`() {
        // A debug build has English and its accented pseudolocale; a phone in British English gets plain English.
        assertEquals("en", shownLanguage(locales("en-GB"), locales("en", "en-XA", "ar-XB")).toLanguageTag())
        assertEquals("en", shownLanguage(locales("en-GB"), locales("en-XA", "en")).toLanguageTag())
        assertEquals("en-XA", shownLanguage(locales("en-XA"), locales("en", "en-XA", "ar-XB")).toLanguageTag())
    }

    @Test fun `Chinese in one script is never shown for a phone in the other`() {
        assertEquals("zh-Hant", shownLanguage(locales("zh-Hant-TW"), locales("en", "zh-Hans", "zh-Hant")).toLanguageTag())
        assertEquals("zh-Hans", shownLanguage(locales("zh-Hans-CN"), locales("en", "zh-Hans", "zh-Hant")).toLanguageTag())
    }

    // --- How the list names each language ---

    @Test fun `English is named once, as it is the same in English`() {
        assertEquals(AppLanguage("en", "English", null), appLanguage(Locale.ENGLISH))
    }

    @Test fun `another language is named in its own script, with its English name under it`() {
        val hindi = appLanguage(Locale.forLanguageTag("hi"))

        assertEquals("hi", hindi.tag)
        assertEquals("Hindi", hindi.englishName)
        assertTrue("in Devanagari: ${hindi.ownName}", hindi.ownName.all { Character.UnicodeBlock.of(it) == Character.UnicodeBlock.DEVANAGARI })
    }

    @Test fun `a name that starts in lower case in its language starts in upper case on its own line`() {
        assertEquals(AppLanguage("es", "Español", "Spanish"), appLanguage(Locale.forLanguageTag("es")))
    }

    @Test fun `the list puts the language shown first, then the rest by their English names`() {
        val list = languageChoices(locales("en", "sw", "hi", "es"), shown = Locale.forLanguageTag("hi"))

        assertEquals(listOf("hi", "en", "es", "sw"), list.map { it.tag })
    }

    @Test fun `an app with one language lists that one`() {
        assertEquals(listOf(AppLanguage("en", "English", null)), languageChoices(locales("en"), shown = Locale.ENGLISH))
    }

    // --- What a tap does ---

    private class FakeSettings : LanguageSettings {
        val set = mutableListOf<String>()
        var opened = 0

        override fun setAppLanguage(tag: String) {
            set += tag
        }

        override fun openPhoneLanguageSettings() {
            opened++
        }
    }

    @Test fun `on Android 13 and up the row opens the app's own list, and a choice goes to the locale manager`() {
        val settings = FakeSettings()
        val row = LanguageRowController(Build.VERSION_CODES.TIRAMISU, settings)

        assertTrue("the list opens", row.tap())
        assertEquals("the phone's settings stay shut", 0, settings.opened)

        row.choose("hi", shown = "en")

        assertEquals(listOf("hi"), settings.set)
    }

    @Test fun `choosing the language already shown changes nothing`() {
        val settings = FakeSettings()

        LanguageRowController(Build.VERSION_CODES.TIRAMISU, settings).choose("en", shown = "en")

        assertEquals(emptyList<String>(), settings.set)
    }

    @Test fun `on Android 8 to 12 the row opens the phone's language settings, with no list`() {
        for (sdk in Build.VERSION_CODES.O..Build.VERSION_CODES.S_V2) {
            val settings = FakeSettings()
            val row = LanguageRowController(sdk, settings)

            assertFalse("no list on $sdk", row.tap())
            assertEquals("the phone's settings open on $sdk", 1, settings.opened)
            assertTrue("and the row says so on $sdk", row.opensPhoneSettings)
        }
    }

    @Test fun `below Android 13 a choice never reaches the locale manager, which those versions lack`() {
        val settings = FakeSettings()

        LanguageRowController(Build.VERSION_CODES.S_V2, settings).choose("hi", shown = "en")

        assertEquals(emptyList<String>(), settings.set)
    }

    @Test fun `on Android 13 and up the row has no note about the phone's settings`() {
        assertFalse(LanguageRowController(Build.VERSION_CODES.TIRAMISU, FakeSettings()).opensPhoneSettings)
    }
}
