package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.ui.language.availableLanguages
import org.onebusaway.vehicletracker.ui.language.preferredLocales

// The design's text fields have 8 dp corners; no other component here uses extraSmall.
private val AppShapes = Shapes(extraSmall = RoundedCornerShape(8.dp))

// Outside AppTheme these are the light set, as MaterialTheme's own colours are: ScreenFlowTest draws
// the screens without a theme around them.
private val LocalStatusColors = staticCompositionLocalOf { LightStatusColors }
private val LocalProblemColors = staticCompositionLocalOf { LightProblemColors }

@Composable
fun AppTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    CompositionLocalProvider(
        LocalStatusColors provides if (darkTheme) DarkStatusColors else LightStatusColors,
        LocalProblemColors provides if (darkTheme) DarkProblemColors else LightProblemColors,
    ) {
        MaterialTheme(
            colorScheme = if (darkTheme) DarkColors else LightColors,
            typography = appTypography(preferredLocales(), availableLanguages()),
            shapes = AppShapes,
            content = content,
        )
    }
}

/** The theme's colours that [MaterialTheme] has no role for. */
object AppTheme {
    val statusColors: StatusColors
        @Composable @ReadOnlyComposable
        get() = LocalStatusColors.current

    val problemColors: ProblemColors
        @Composable @ReadOnlyComposable
        get() = LocalProblemColors.current
}
