package org.onebusaway.vehicletracker.ui.routes

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R

private const val HEX_DIGITS = 6
private const val OPAQUE_ALPHA = 0xFF000000L

/**
 * A GTFS route colour as opaque ARGB. The feed writes six hex digits with no leading `#`, and
 * `route_color` is optional, so null here means "the feed gave none, or gave something
 * unreadable" and leaves the badge on the theme's own colours.
 */
fun routeColorArgb(hex: String): Long? {
    val digits = hex.removePrefix("#")
    if (digits.length != HEX_DIGITS) return null
    val value = digits.toLongOrNull(radix = 16) ?: return null
    return OPAQUE_ALPHA or value
}

/**
 * A route's short name on its GTFS colour, as riders see it. [color] and [textColor] are the feed's six hex
 * digits; the route list reads them off the route, and the Tracking screen off the trip it keeps. The trip
 * bar draws it larger, in [style] and [shape], centred in whatever size [modifier] gives it.
 */
@Composable
fun RouteBadge(
    shortName: String,
    color: String,
    textColor: String,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.titleMedium,
    shape: Shape = RoundedCornerShape(6.dp),
) {
    val background = routeColorArgb(color)?.let { Color(it) } ?: MaterialTheme.colorScheme.primary
    val foreground = routeColorArgb(textColor)?.let { Color(it) } ?: MaterialTheme.colorScheme.onPrimary
    Box(
        modifier = modifier
            .background(background, shape)
            .padding(horizontal = 10.dp, vertical = 4.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = shortName.ifEmpty { stringResource(R.string.routes_badge_placeholder) },
            color = foreground,
            style = style,
            fontWeight = FontWeight.SemiBold,
            maxLines = 1,
        )
    }
}
