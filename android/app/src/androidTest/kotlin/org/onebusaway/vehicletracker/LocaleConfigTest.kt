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
 * generates. English is the app's one language so far; a debug build adds the two pseudolocales.
 */
@RunWith(AndroidJUnit4::class)
class LocaleConfigTest {
    @Test
    fun theAppListsEnglish_andInADebugBuildThePseudolocales() {
        assumeTrue("Android reads an app's languages from 13", Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)

        val config = LocaleConfig(InstrumentationRegistry.getInstrumentation().targetContext)

        assertEquals(LocaleConfig.STATUS_SUCCESS, config.status)
        val locales = checkNotNull(config.supportedLocales)
        assertEquals(setOf("en", "en-XA", "ar-XB"), (0 until locales.size()).map { locales[it].toLanguageTag() }.toSet())
    }
}
