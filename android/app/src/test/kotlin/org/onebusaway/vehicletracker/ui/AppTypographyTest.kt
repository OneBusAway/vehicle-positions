package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.text.font.FontFamily
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test
import org.onebusaway.vehicletracker.ui.theme.appTypography
import java.util.Locale

/** The bundled font has Latin letters alone, so a language in another script is set in the system font throughout. */
class AppTypographyTest {
    @Test fun `English and its pseudolocale keep the app's own font`() {
        for (tag in listOf("en", "en-US", "en-XA")) {
            val family = appTypography(Locale.forLanguageTag(tag)).bodyLarge.fontFamily
            assertNotEquals(tag, FontFamily.Default, family)
        }
    }

    @Test fun `Hindi sets every style in the system font, with tabular digits still`() {
        val typography = appTypography(Locale.forLanguageTag("hi-IN"))
        val styles = listOf(
            typography.displayLarge, typography.headlineLarge, typography.headlineSmall, typography.titleMedium,
            typography.bodyLarge, typography.bodyMedium, typography.labelLarge, typography.labelSmall,
        )

        for (style in styles) {
            assertEquals(FontFamily.Default, style.fontFamily)
            assertEquals("tnum", style.fontFeatureSettings)
        }
    }
}
