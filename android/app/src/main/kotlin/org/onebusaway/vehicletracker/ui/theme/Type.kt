package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import org.onebusaway.vehicletracker.R

/**
 * Google Sans Flex v5.000 (OFL, see `assets/licenses`) from google/fonts `ofl/googlesansflex` at bd8f81d, cut to
 * weights 400 to 600 and Latin with fontTools 4.60. Bundled, not downloadable: drivers start shifts with no signal.
 * ```
 * fonttools varLib.instancer "GoogleSansFlex[ROND,opsz,slnt,wdth,wght].ttf" wght=400:600 wdth=100 slnt=0 ROND=0 opsz=drop -o gsf-wght.ttf
 * pyftsubset gsf-wght.ttf --output-file=google_sans_flex.ttf --unicodes="U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD" --layout-features+=tnum,lnum,pnum,zero --name-IDs='*' --name-languages='*' --notdef-outline
 * ```
 */
internal val AppFontFamily = FontFamily(
    Font(R.font.google_sans_flex, FontWeight.Normal, variationSettings = FontVariation.Settings(FontVariation.weight(400))),
    Font(R.font.google_sans_flex, FontWeight.Medium, variationSettings = FontVariation.Settings(FontVariation.weight(500))),
    Font(R.font.google_sans_flex, FontWeight.SemiBold, variationSettings = FontVariation.Settings(FontVariation.weight(600))),
)

private val base = Typography()

// Material's own sizes, weights and line heights, which the design uses. Tabular digits keep the ticking trip
// time and sent count from wobbling, and the design sets no letter spacing.
private fun TextStyle.inAppFont() = copy(fontFamily = AppFontFamily, fontFeatureSettings = "tnum", letterSpacing = 0.sp)

internal val AppTypography = Typography(
    displayLarge = base.displayLarge.inAppFont(),
    displayMedium = base.displayMedium.inAppFont(),
    displaySmall = base.displaySmall.inAppFont(),
    headlineLarge = base.headlineLarge.inAppFont(),
    headlineMedium = base.headlineMedium.inAppFont(),
    headlineSmall = base.headlineSmall.inAppFont(),
    titleLarge = base.titleLarge.inAppFont(),
    titleMedium = base.titleMedium.inAppFont(),
    titleSmall = base.titleSmall.inAppFont(),
    bodyLarge = base.bodyLarge.inAppFont(),
    bodyMedium = base.bodyMedium.inAppFont(),
    bodySmall = base.bodySmall.inAppFont(),
    labelLarge = base.labelLarge.inAppFont(),
    labelMedium = base.labelMedium.inAppFont(),
    labelSmall = base.labelSmall.inAppFont(),
)

// The design's three sizes that Material has no role for, read like the others: MaterialTheme.typography.statusHero.

/** The status card's answer, "On time" or "6 min late". The card shrinks it to 28 sp rather than wrap it. */
internal val Typography.statusHero: TextStyle
    get() = headlineLarge.copy(fontSize = 40.sp, lineHeight = 46.sp, fontWeight = FontWeight.Medium)

/** The status card's two lines under the answer. */
internal val Typography.statusLine: TextStyle
    get() = bodyLarge.copy(fontSize = 18.sp, lineHeight = 24.sp)

/** The trip's headsign on the trip bar, and the next stop's name on the status card. */
internal val Typography.tripTitle: TextStyle
    get() = titleMedium.copy(fontSize = 18.sp, lineHeight = 24.sp)
