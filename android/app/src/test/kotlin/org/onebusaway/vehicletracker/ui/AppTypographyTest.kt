package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.text.font.FontFamily
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test
import org.onebusaway.vehicletracker.ui.theme.appTypography
import java.util.Locale

/**
 * The font follows the language the app shows, not the phone's: the bundled font has Latin letters alone, so Hindi is
 * set in the system font throughout, and the English a phone in a language the app lacks is shown keeps the bundled
 * font.
 */
class AppTypographyTest {
    private fun locales(vararg tags: String) = tags.map(Locale::forLanguageTag)

    private val available = locales("en", "hi")

    @Test fun `a phone in a language the app lacks is shown English in the app's own font`() {
        for (tag in listOf("es-US", "de-DE", "fr-FR", "mr-IN")) {
            val family = appTypography(locales(tag), available).bodyLarge.fontFamily
            assertNotEquals(tag, FontFamily.Default, family)
        }
    }

    @Test fun `English and its pseudolocales keep the app's own font`() {
        for (tag in listOf("en", "en-US", "en-XA", "ar-XB")) {
            val family = appTypography(locales(tag), available + locales("en-XA", "ar-XB")).bodyLarge.fontFamily
            assertNotEquals(tag, FontFamily.Default, family)
        }
    }

    @Test fun `Hindi sets every style in the system font, with tabular digits still`() {
        val typography = appTypography(locales("hi-IN"), available)
        val styles = listOf(
            typography.displayLarge, typography.headlineLarge, typography.headlineSmall, typography.titleMedium,
            typography.bodyLarge, typography.bodyMedium, typography.labelLarge, typography.labelSmall,
        )

        for (style in styles) {
            assertEquals(FontFamily.Default, style.fontFamily)
            assertEquals("tnum", style.fontFeatureSettings)
        }
    }

    @Test fun `a phone that lists Hindi after a language the app lacks is shown Hindi, in the system font`() {
        assertEquals(FontFamily.Default, appTypography(locales("es-US", "hi-IN"), available).bodyLarge.fontFamily)
    }

    @Test fun `Arabic is set in the system font, and its pseudolocale in the app's own`() {
        val available = locales("en", "ar", "ar-XB")
        assertEquals(FontFamily.Default, appTypography(locales("ar-EG"), available).bodyLarge.fontFamily)
        assertNotEquals(FontFamily.Default, appTypography(locales("ar-XB"), available).bodyLarge.fontFamily)
    }
}
