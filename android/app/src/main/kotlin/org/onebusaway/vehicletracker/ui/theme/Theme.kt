package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

val StatusGreen = Color(0xFF1B873B)
val StatusRed = Color(0xFFC62828)

private val LightColors = lightColorScheme(
    primary = Color(0xFF0B5394),
    onPrimary = Color.White,
)
private val DarkColors = darkColorScheme(
    primary = Color(0xFF7BB6E8),
    onPrimary = Color(0xFF00253D),
)

@Composable
fun AppTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColors else LightColors,
        content = content,
    )
}
