package org.onebusaway.vehicletracker.ui.routes

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.engine.TripGeometry

private const val HEX_DIGITS = 6
private const val OPAQUE_ALPHA = 0xFF000000L
private const val RGB_MASK = 0xFFFFFF

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

/** [color] back as the feed writes it, six hex digits, or "" for none: for handing a badge on to another screen. */
fun routeColorHex(color: Color?): String =
    color?.let { (it.toArgb() and RGB_MASK).toString(radix = 16).padStart(HEX_DIGITS, '0').uppercase() }.orEmpty()

/**
 * A route's short name and colours with the feed's hex already read, so a list of badges parses nothing while it
 * scrolls. A null colour leaves the badge on the theme's own, as [routeColorArgb] explains.
 */
@Immutable
data class RouteBadgeModel(val shortName: String, val color: Color?, val textColor: Color?) {
    companion object {
        /** [color] and [textColor] as the feed writes them: six hex digits. */
        fun of(shortName: String, color: String, textColor: String) = RouteBadgeModel(
            shortName = shortName,
            color = routeColorArgb(color)?.let { Color(it) },
            textColor = routeColorArgb(textColor)?.let { Color(it) },
        )

        /**
         * The route of the trip the phone has open, from its stored [geometry]; its route id on the theme's colours
         * when there is no geometry, as for a trip started by an older build, or the geometry names no route.
         */
        fun forTrip(trip: ActiveTrip, geometry: TripGeometry?) = of(
            shortName = geometry?.routeShortName?.ifEmpty { null } ?: trip.routeId,
            color = geometry?.routeColor.orEmpty(),
            textColor = geometry?.routeTextColor.orEmpty(),
        )
    }
}

/**
 * A route's short name on its GTFS colour, as riders see it. [color] and [textColor] are the feed's six hex
 * digits; the Tracking screen reads them off the trip it keeps. The trip bar draws it larger, in [style] and
 * [shape], centred in whatever size [modifier] gives it.
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
    RouteBadge(RouteBadgeModel.of(shortName, color, textColor), modifier, style, shape)
}

/** [route] as a badge, for a screen that read its routes' colours once when they loaded. */
@Composable
fun RouteBadge(
    route: RouteBadgeModel,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.titleMedium,
    shape: Shape = RoundedCornerShape(6.dp),
) {
    val background = route.color ?: MaterialTheme.colorScheme.primary
    val foreground = route.textColor ?: MaterialTheme.colorScheme.onPrimary
    Box(
        modifier = modifier
            .background(background, shape)
            .padding(horizontal = 10.dp, vertical = 4.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = route.shortName.ifEmpty { stringResource(R.string.routes_badge_placeholder) },
            color = foreground,
            style = style,
            fontWeight = FontWeight.SemiBold,
            maxLines = 1,
        )
    }
}
