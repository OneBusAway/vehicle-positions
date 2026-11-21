package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.language.shownLanguage
import java.util.Locale

/**
 * Google Sans Flex v5.000 (OFL, see `assets/licenses`) from google/fonts `ofl/googlesansflex` at bd8f81d, cut to
 * weights 400 to 600 and to Latin with Google Fonts' latin-ext and vietnamese subsets, with fontTools 4.60. Bundled,
 * not downloadable: drivers start shifts with no signal.
 * ```
 * fonttools varLib.instancer "GoogleSansFlex[ROND,opsz,slnt,wdth,wght].ttf" wght=400:600 wdth=100 slnt=0 ROND=0 opsz=drop -o gsf-wght.ttf
 * pyftsubset gsf-wght.ttf --output-file=google_sans_flex.ttf --unicodes="U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD,U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF,U+0300-0301,U+0303,U+0309,U+0323,U+1EA0-1EF9" --layout-features+=tnum,lnum,pnum,zero --name-IDs='*' --name-languages='*' --notdef-outline
 * ```
 * A character the font has no glyph for, such as the run list's → or a letter of the en-XA pseudolocale of a debug
 * build, falls back to the system font on its own. A language in a script the font lacks, as Hindi's Devanagari is,
 * sets all its text in the system font ([systemFontLanguages]), and FontCoverageTest holds every other language to
 * having all its letters in this font.
 */
internal val AppFontFamily = FontFamily(
    Font(R.font.google_sans_flex, FontWeight.Normal, variationSettings = FontVariation.Settings(FontVariation.weight(400))),
    Font(R.font.google_sans_flex, FontWeight.Medium, variationSettings = FontVariation.Settings(FontVariation.weight(500))),
    Font(R.font.google_sans_flex, FontWeight.SemiBold, variationSettings = FontVariation.Settings(FontVariation.weight(600))),
)

private val base = Typography()

// Material's own sizes, weights and line heights, which the design uses. Tabular digits keep the ticking trip
// time and sent count from wobbling, and the design sets no letter spacing.
private fun TextStyle.inFont(family: FontFamily) = copy(fontFamily = family, fontFeatureSettings = "tnum", letterSpacing = 0.sp)

private fun typographyIn(family: FontFamily) = Typography(
    displayLarge = base.displayLarge.inFont(family),
    displayMedium = base.displayMedium.inFont(family),
    displaySmall = base.displaySmall.inFont(family),
    headlineLarge = base.headlineLarge.inFont(family),
    headlineMedium = base.headlineMedium.inFont(family),
    headlineSmall = base.headlineSmall.inFont(family),
    titleLarge = base.titleLarge.inFont(family),
    titleMedium = base.titleMedium.inFont(family),
    titleSmall = base.titleSmall.inFont(family),
    bodyLarge = base.bodyLarge.inFont(family),
    bodyMedium = base.bodyMedium.inFont(family),
    bodySmall = base.bodySmall.inFont(family),
    labelLarge = base.labelLarge.inFont(family),
    labelMedium = base.labelMedium.inFont(family),
    labelSmall = base.labelSmall.inFont(family),
)

private val AppTypography = typographyIn(AppFontFamily)
private val SystemTypography = typographyIn(FontFamily.Default)

// The languages in a script the font above lacks: Devanagari, Ethiopic, Japanese, Khmer, Hangul, Lao, Gurmukhi,
// Cyrillic, Chinese and Arabic. Their text is set all in the system font (for Hindi, the Noto Sans Devanagari Android
// ships), so a line never mixes this font's digits and Latin words with the system's letters. Every other language
// keeps the font: English, the English a phone in a language the app lacks is shown, and every Latin-script
// translation. FontCoverageTest fails a language missing from here whose letters the font lacks.
internal val systemFontLanguages = setOf(
    "hi",
    "am", "ja", "km", "ko", "lo", "pa", "ru", "ti", "uk", "zh",
    "ar", "fa",
)

// The pseudolocales of a debug build, en-XA and ar-XB, are for checking layout, so they keep the font the app ships
// with even where their language is set in the system font.
private val pseudolocaleRegions = setOf("XA", "XB")

/**
 * The typography for the language the app shows a phone or app set to [preferred], of the app's [available]
 * languages, chosen as Android chooses its strings ([shownLanguage]): the app's own font, or the system's for a script
 * it lacks. A phone set to Spanish, which the app lacks, is shown English, in the app's own font.
 */
internal fun appTypography(preferred: List<Locale>, available: List<Locale>): Typography {
    val shown = shownLanguage(preferred, available)
    val systemFont = shown.language in systemFontLanguages && shown.country !in pseudolocaleRegions
    return if (systemFont) SystemTypography else AppTypography
}

// The design's styles that Material has no role for, read like the others: MaterialTheme.typography.statusHero.

/** The status card's answer, "On time" or "6 min late". The card shrinks it to 28 sp rather than wrap it. */
internal val Typography.statusHero: TextStyle
    get() = headlineLarge.copy(fontSize = 40.sp, lineHeight = 46.sp, fontWeight = FontWeight.Medium)

/** The status card's two lines under the answer. */
internal val Typography.statusLine: TextStyle
    get() = bodyLarge.copy(fontSize = 18.sp, lineHeight = 24.sp)

/** The trip's headsign on the trip bar, and the next stop's name on the status card. */
internal val Typography.tripTitle: TextStyle
    get() = titleMedium.copy(fontSize = 18.sp, lineHeight = 24.sp)

/** A bus's fleet number, big on its tile, which is what the driver reads off the bus. */
internal val Typography.fleetNumber: TextStyle
    get() = headlineLarge.copy(fontWeight = FontWeight.Medium)

/** A run's start time on the departure board. */
internal val Typography.departureTime: TextStyle
    get() = headlineSmall.copy(fontWeight = FontWeight.Medium)
