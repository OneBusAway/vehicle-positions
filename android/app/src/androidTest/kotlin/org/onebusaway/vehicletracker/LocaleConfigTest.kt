package org.onebusaway.vehicletracker

import android.app.LocaleConfig
import android.os.Build
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The languages the app lists for Settings > Apps > Language, which Android reads from the locale config the build
 * generates from its values-<language> folders: English and each language the app is translated into, and in a debug
 * build the two pseudolocales.
 */
@RunWith(AndroidJUnit4::class)
class LocaleConfigTest {
    private val languages = setOf(
        "en",
        "hi",
        "de", "es", "fi", "fil", "fr", "ht", "it", "om", "pl", "pt-BR", "sm", "so", "sw", "vi",
    )

    @Test
    fun theAppListsItsLanguages_andInADebugBuildThePseudolocales() {
        assumeTrue("Android reads an app's languages from 13", Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)

        val config = LocaleConfig(InstrumentationRegistry.getInstrumentation().targetContext)

        assertEquals(LocaleConfig.STATUS_SUCCESS, config.status)
        val locales = checkNotNull(config.supportedLocales)
        assertEquals(languages + setOf("en-XA", "ar-XB"), (0 until locales.size()).map { locales[it].toLanguageTag() }.toSet())
    }
}
