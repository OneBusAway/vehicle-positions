package org.onebusaway.vehicletracker.ui.components

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/**
 * A dialog's main action, filled in ink rather than a theme colour, which the screens behind give a meaning: red is
 * early on Tracking. While [busy] it shows a spinner and takes no taps, and keeps its colour so the spinner shows.
 */
@Composable
internal fun InkButton(text: String, onClick: () -> Unit, busy: Boolean = false) {
    val ink = MaterialTheme.colorScheme.onSurface
    val paper = MaterialTheme.colorScheme.surface
    Button(
        onClick = onClick,
        enabled = !busy,
        colors = ButtonDefaults.buttonColors(
            containerColor = ink,
            contentColor = paper,
            disabledContainerColor = ink,
            disabledContentColor = paper,
        ),
    ) {
        if (busy) {
            CircularProgressIndicator(
                color = LocalContentColor.current,
                strokeWidth = 2.dp,
                modifier = Modifier.size(16.dp),
            )
            Spacer(Modifier.width(8.dp))
        }
        Text(text)
    }
}

/** A dialog's other action, in ink rather than the theme's green. */
@Composable
internal fun QuietButton(text: String, onClick: () -> Unit, enabled: Boolean = true) {
    TextButton(
        onClick = onClick,
        enabled = enabled,
        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.onSurface),
    ) {
        Text(text)
    }
}
