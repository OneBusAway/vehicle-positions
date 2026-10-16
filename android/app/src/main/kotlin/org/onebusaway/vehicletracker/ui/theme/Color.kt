package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color

internal val LightColors = lightColorScheme(
    // OneBusAway Android's own M3 scheme, which the approved design uses.
    primary = Color(0xFF3B6939),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFBCF0B4),
    onPrimaryContainer = Color(0xFF235024),
    secondary = Color(0xFF436833),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFC4EFAC),
    onSecondaryContainer = Color(0xFF2C4F1D),
    error = Color(0xFFBA1A1A),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFFFDAD6),
    onErrorContainer = Color(0xFF93000A),
    // material-color-utilities TonalSpot from OneBusAway's seed #4CAF50, which reproduces its primary and error.
    inversePrimary = Color(0xFFA1D39A),
    tertiary = Color(0xFF38656A),
    onTertiary = Color(0xFFFFFFFF),
    tertiaryContainer = Color(0xFFBCEBF0),
    onTertiaryContainer = Color(0xFF1F4D52),
    primaryFixed = Color(0xFFBCF0B4),
    primaryFixedDim = Color(0xFFA1D39A),
    onPrimaryFixed = Color(0xFF002204),
    onPrimaryFixedVariant = Color(0xFF235024),
    tertiaryFixed = Color(0xFFBCEBF0),
    tertiaryFixedDim = Color(0xFFA0CFD4),
    onTertiaryFixed = Color(0xFF002023),
    onTertiaryFixedVariant = Color(0xFF1F4D52),
    // OneBusAway's secondary palette (tones 90, 80, 10, 30): TonalSpot's own secondary is a different, greyer one.
    secondaryFixed = Color(0xFFC4EFAC),
    secondaryFixedDim = Color(0xFFA8D292),
    onSecondaryFixed = Color(0xFF052100),
    onSecondaryFixedVariant = Color(0xFF2C4F1D),
    // The design's neutral greys.
    background = Color(0xFFFFFFFF),
    onBackground = Color(0xFF1F1F1F),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF1F1F1F),
    onSurfaceVariant = Color(0xFF444746),
    outline = Color(0xFF747775),
    outlineVariant = Color(0xFFC4C7C5),
    surfaceContainer = Color(0xFFF1F3F4),
    surfaceContainerHigh = Color(0xFFE8EAED),
    surfaceContainerHighest = Color(0xFFE3E5E8),
    // The roles the design does not draw, from the same greys.
    surfaceVariant = Color(0xFFE1E3E1),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF8F9FA),
    surfaceDim = Color(0xFFDADCE0),
    surfaceBright = Color(0xFFFFFFFF),
    inverseSurface = Color(0xFF303030),
    inverseOnSurface = Color(0xFFF2F2F2),
    // M3's defaults.
    surfaceTint = Color(0xFF3B6939),
    scrim = Color(0xFF000000),
)

internal val DarkColors = darkColorScheme(
    // OneBusAway Android's own M3 scheme, which the approved design uses.
    primary = Color(0xFFA1D39A),
    onPrimary = Color(0xFF0A390F),
    primaryContainer = Color(0xFF235024),
    onPrimaryContainer = Color(0xFFBCF0B4),
    secondary = Color(0xFFA8D292),
    onSecondary = Color(0xFF153808),
    secondaryContainer = Color(0xFF2C4F1D),
    onSecondaryContainer = Color(0xFFC4EFAC),
    error = Color(0xFFFFB4AB),
    onError = Color(0xFF690005),
    errorContainer = Color(0xFF93000A),
    onErrorContainer = Color(0xFFFFDAD6),
    // material-color-utilities TonalSpot from OneBusAway's seed #4CAF50, which reproduces its primary and error.
    inversePrimary = Color(0xFF3B6939),
    tertiary = Color(0xFFA0CFD4),
    onTertiary = Color(0xFF00363B),
    tertiaryContainer = Color(0xFF1F4D52),
    onTertiaryContainer = Color(0xFFBCEBF0),
    primaryFixed = Color(0xFFBCF0B4),
    primaryFixedDim = Color(0xFFA1D39A),
    onPrimaryFixed = Color(0xFF002204),
    onPrimaryFixedVariant = Color(0xFF235024),
    tertiaryFixed = Color(0xFFBCEBF0),
    tertiaryFixedDim = Color(0xFFA0CFD4),
    onTertiaryFixed = Color(0xFF002023),
    onTertiaryFixedVariant = Color(0xFF1F4D52),
    // OneBusAway's secondary palette (tones 90, 80, 10, 30): TonalSpot's own secondary is a different, greyer one.
    secondaryFixed = Color(0xFFC4EFAC),
    secondaryFixedDim = Color(0xFFA8D292),
    onSecondaryFixed = Color(0xFF052100),
    onSecondaryFixedVariant = Color(0xFF2C4F1D),
    // The design's neutral greys.
    background = Color(0xFF131314),
    onBackground = Color(0xFFE3E3E3),
    surface = Color(0xFF131314),
    onSurface = Color(0xFFE3E3E3),
    onSurfaceVariant = Color(0xFFC4C7C5),
    outline = Color(0xFF8E918F),
    outlineVariant = Color(0xFF444746),
    surfaceContainer = Color(0xFF1E1F20),
    surfaceContainerHigh = Color(0xFF2A2B2D),
    surfaceContainerHighest = Color(0xFF3A3B3E),
    // The roles the design does not draw, from the same greys.
    surfaceVariant = Color(0xFF444746),
    surfaceContainerLowest = Color(0xFF0E0E0F),
    surfaceContainerLow = Color(0xFF1B1B1C),
    surfaceDim = Color(0xFF131314),
    surfaceBright = Color(0xFF37393B),
    inverseSurface = Color(0xFFE3E3E3),
    inverseOnSurface = Color(0xFF303030),
    // M3's defaults.
    surfaceTint = Color(0xFFA1D39A),
    scrim = Color(0xFF000000),
)

enum class StatusTone { ON_TIME, LATE, EARLY, NEUTRAL }

/** One schedule status: [fill] behind [onFill] on the status card, [text] for words and icons on a surface. */
@Immutable
data class StatusColor(val fill: Color, val onFill: Color, val text: Color)

@Immutable
data class StatusColors(val onTime: StatusColor, val late: StatusColor, val early: StatusColor, val neutral: StatusColor) {
    operator fun get(tone: StatusTone): StatusColor = when (tone) {
        StatusTone.ON_TIME -> onTime
        StatusTone.LATE -> late
        StatusTone.EARLY -> early
        StatusTone.NEUTRAL -> neutral
    }
}

// The text tier is OneBusAway Android's in both themes, which it took from iOS and adjusted to read as small
// text. The fills are the design's card style 2a, the same in both themes and darker than OneBusAway's, so
// white text on them is 7:1 and reads at a glance in sun.
internal val LightStatusColors = StatusColors(
    onTime = StatusColor(fill = Color(0xFF006600), onFill = Color(0xFFFFFFFF), text = Color(0xFF0E7400)),
    late = StatusColor(fill = Color(0xFF0055B3), onFill = Color(0xFFFFFFFF), text = Color(0xFF0060C9)),
    early = StatusColor(fill = Color(0xFFA51D17), onFill = Color(0xFFFFFFFF), text = Color(0xFFBA2B23)),
    neutral = StatusColor(fill = Color(0xFF58585C), onFill = Color(0xFFFFFFFF), text = Color(0xFF636367)),
)

internal val DarkStatusColors = StatusColors(
    onTime = LightStatusColors.onTime.copy(text = Color(0xFF30D158)),
    late = LightStatusColors.late.copy(text = Color(0xFF0A8BFF)),
    early = LightStatusColors.early.copy(text = Color(0xFFFF453A)),
    neutral = LightStatusColors.neutral.copy(text = Color(0xFF8E8E93)),
)

/**
 * A reporting problem (no network, no GPS): a calm dark line with an amber [icon], because red already means
 * early. [iconOnSurface] is the same warning on the trip bar and in a dialog, and [signedOut] is the card that
 * takes the status card's place once reports have stopped.
 */
@Immutable
data class ProblemColors(
    val container: Color,
    val content: Color,
    val icon: Color,
    val iconOnSurface: Color,
    val signedOut: SignedOutColors,
)

/** [content] on [container], and the icon disc and Sign in button in [accent] with [onAccent] on them. */
@Immutable
data class SignedOutColors(val container: Color, val content: Color, val accent: Color, val onAccent: Color)

private val Amber = Color(0xFFFDD663)
private val Ink = Color(0xFF1F1F1F)

internal val LightProblemColors = ProblemColors(
    container = Color(0xFF303134),
    content = Color(0xFFF1F3F4),
    icon = Amber,
    iconOnSurface = Color(0xFFB06000),
    signedOut = SignedOutColors(container = Ink, content = Color(0xFFF1F3F4), accent = Amber, onAccent = Ink),
)

// An ink card would sink into the dark map, so in the dark theme Signed out turns amber with ink on it.
internal val DarkProblemColors = ProblemColors(
    container = Color(0xFF3C3D40),
    content = Color(0xFFF1F3F4),
    icon = Amber,
    iconOnSurface = Amber,
    signedOut = SignedOutColors(container = Amber, content = Ink, accent = Ink, onAccent = Amber),
)
