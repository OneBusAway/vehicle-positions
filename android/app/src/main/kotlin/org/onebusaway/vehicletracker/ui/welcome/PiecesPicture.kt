package org.onebusaway.vehicletracker.ui.welcome

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.theme.AppTheme

private val LiveShape = RoundedCornerShape(24.dp)

// Each holder is the shape its step's own piece has: a vehicle's tile, a route's badge and a run's clock.
private val VehicleHolder = RoundedCornerShape(14.dp)
private val RouteHolder = RoundedCornerShape(12.dp)

/**
 * Screen 2's picture, in the app's own pieces: the three choices fill in one after another, then a green card says
 * riders see the vehicle, then End trip. It is a picture all through. No piece is a button, and nothing here is
 * recomposed while it plays: each piece sits in its place from the first frame and [clock] moves its layer.
 */
@Composable
internal fun PiecesPicture(clock: PictureClock, compact: Boolean, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(if (compact) 6.dp else 10.dp)) {
        PieceCard(0, clock, compact, R.drawable.ic_directions_bus, VehicleHolder, R.string.vehicles_title, R.string.welcome_pieces_vehicle_hint)
        PieceCard(1, clock, compact, R.drawable.ic_route, RouteHolder, R.string.routes_title, R.string.welcome_pieces_route_hint)
        PieceCard(2, clock, compact, R.drawable.ic_schedule_outline, CircleShape, R.string.runs_title, R.string.welcome_pieces_run_hint)
        LiveCard(clock, compact)
        FinishRow(clock, compact)
    }
}

/** One choice: outlined and grey as it rises in, then filled, its icon lit and a check at its end. */
@Composable
private fun PieceCard(
    index: Int,
    clock: PictureClock,
    compact: Boolean,
    @DrawableRes icon: Int,
    holder: Shape,
    @StringRes title: Int,
    @StringRes hint: Int,
) {
    val colors = MaterialTheme.colorScheme
    val filled = colors.surfaceContainer
    val outline = colors.outlineVariant
    val fillsAt = PiecesTimeline.cardFillsAt(index)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .risesIn(clock, PiecesTimeline.cardRisesAt(index))
            .drawWithCache {
                val edge = Stroke(1.5.dp.toPx())
                val corner = CornerRadius(20.dp.toPx())
                val edgeAt = Offset(edge.width / 2, edge.width / 2)
                val edgeSize = Size(size.width - edge.width, size.height - edge.width)
                onDrawBehind {
                    val fill = WelcomeMotion.Fade.settle(clock.millis - fillsAt)
                    drawRoundRect(filled, cornerRadius = corner, alpha = fill)
                    drawRoundRect(outline, edgeAt, edgeSize, corner, edge, alpha = 1f - fill)
                }
            }
            .heightIn(min = if (compact) 56.dp else 72.dp)
            .padding(horizontal = if (compact) 14.dp else 16.dp, vertical = if (compact) 6.dp else 12.dp)
            // One element for TalkBack, the title then the hint.
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        PieceIcon(icon, holder, clock, fillsAt, compact)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(stringResource(title), style = MaterialTheme.typography.titleMedium)
            Text(stringResource(hint), style = MaterialTheme.typography.bodyMedium, color = colors.onSurfaceVariant)
        }
        Box(
            modifier = Modifier
                .size(28.dp)
                .graphicsLayer {
                    val since = clock.millis - fillsAt
                    val grown = 0.5f + 0.5f * WelcomeMotion.Pop.settle(since)
                    alpha = WelcomeMotion.Fade.settle(since)
                    scaleX = grown
                    scaleY = grown
                }
                .background(colors.primary, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(painterResource(R.drawable.ic_check), contentDescription = null, tint = colors.onPrimary, modifier = Modifier.size(18.dp))
        }
    }
}

/** A choice's icon in its holder, grey until the card fills at [fillsAt] and in the accent container from then. */
@Composable
private fun PieceIcon(@DrawableRes icon: Int, holder: Shape, clock: PictureClock, fillsAt: Float, compact: Boolean) {
    val colors = MaterialTheme.colorScheme
    val off = colors.surfaceContainerHigh
    val on = colors.primaryContainer
    val glyphOff = colors.onSurfaceVariant
    val glyphOn = colors.onPrimaryContainer
    val glyph = painterResource(icon)
    Box(
        modifier = Modifier
            .size(if (compact) 40.dp else 48.dp)
            .clip(holder)
            .drawBehind {
                drawRect(off)
                drawRect(on, alpha = WelcomeMotion.Fade.settle(clock.millis - fillsAt))
            },
        contentAlignment = Alignment.Center,
    ) {
        Spacer(
            Modifier.size(26.dp).drawWithCache {
                val offTint = ColorFilter.tint(glyphOff)
                val onTint = ColorFilter.tint(glyphOn)
                onDrawBehind {
                    val lit = WelcomeMotion.Fade.settle(clock.millis - fillsAt)
                    with(glyph) {
                        draw(size, alpha = 1f - lit, colorFilter = offTint)
                        draw(size, alpha = lit, colorFilter = onTint)
                    }
                }
            },
        )
    }
}

/** The status card's look, saying what the three choices add up to, with the trip bar's sent count ticking up. */
@Composable
private fun LiveCard(clock: PictureClock, compact: Boolean) {
    val onTime = AppTheme.statusColors.onTime
    val words = if (compact) MaterialTheme.typography.titleLarge else MaterialTheme.typography.headlineSmall
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .risesIn(clock, PiecesTimeline.LIVE_RISES_AT)
            .background(onTime.fill, LiveShape)
            .heightIn(min = if (compact) 56.dp else 68.dp)
            .padding(start = 12.dp, top = 12.dp, end = 16.dp, bottom = 12.dp)
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(44.dp).background(onTime.onFill.copy(alpha = 0.16f), CircleShape), contentAlignment = Alignment.Center) {
            Icon(painterResource(R.drawable.ic_check_circle), contentDescription = null, tint = onTime.onFill, modifier = Modifier.size(26.dp))
        }
        Text(
            text = stringResource(R.string.welcome_pieces_live),
            style = words.copy(fontWeight = FontWeight.Medium),
            color = onTime.onFill,
            modifier = Modifier.weight(1f),
        )
        SentCount(clock, onTime.onFill)
    }
}

/**
 * The cloud and "1 sent", then 2 and 3. The three counts sit on top of each other and [clock] shows one, so the count
 * changes without a recomposition. TalkBack is not read a number that only illustrates.
 */
@Composable
private fun SentCount(clock: PictureClock, color: Color) {
    Row(
        modifier = Modifier.clearAndSetSemantics {},
        horizontalArrangement = Arrangement.spacedBy(5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(painterResource(R.drawable.ic_cloud_done), contentDescription = null, tint = color, modifier = Modifier.size(18.dp))
        Box {
            for (count in 1..3) {
                Text(
                    text = pluralStringResource(R.plurals.tracking_bar_sent, count, count),
                    style = MaterialTheme.typography.labelLarge,
                    color = color,
                    maxLines = 1,
                    modifier = Modifier.graphicsLayer { alpha = if (PiecesTimeline.sent(clock.millis) == count) 1f else 0f },
                )
            }
        }
    }
}

/** How a trip ends: the trip bar's End trip, drawn as it looks there. A picture of the button, so not a button. */
@Composable
private fun FinishRow(clock: PictureClock, compact: Boolean) {
    val colors = MaterialTheme.colorScheme
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .risesIn(clock, PiecesTimeline.FINISH_RISES_AT)
            .padding(start = 8.dp, end = 4.dp)
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.welcome_pieces_finish),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Row(
            modifier = Modifier
                .heightIn(min = if (compact) 48.dp else 56.dp)
                .border(1.5.dp, colors.outline, CircleShape)
                .padding(horizontal = 20.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(painterResource(R.drawable.ic_stop_circle), contentDescription = null, tint = colors.onSurface, modifier = Modifier.size(22.dp))
            Text(stringResource(R.string.tracking_end_trip_button), style = MaterialTheme.typography.titleMedium, color = colors.onSurface)
        }
    }
}
