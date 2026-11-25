package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.spring
import androidx.compose.foundation.layout.Spacer
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.remember
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.textOf
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** Under this width the stops are closer, so the vehicle is smaller and the state's words a size down. */
internal val NarrowWidth = 350.dp

private val LineThickness = 6.dp
private val DashThickness = 4.dp
private val VehicleSize = 46.dp
private val RiderSize = 28.dp
private val IconSize = 22.dp
private val IconCentreAboveLine = 35.dp
private val HomeInset = 36.dp
private val HomeInsetNarrow = 30.dp
private val RiderInset = 32.dp
private val VehicleFromRider = 42.dp
private val ClosestIcons = 32.dp

private const val NARROW_VEHICLE = 0.82f
private const val FOLDED_VEHICLE = 0.65f
private const val RIDER_FAR = 0.5f

/** The vehicle has arrived a little before its spring's last settling, so the line's end does not wait for it. */
private const val ARRIVED_WITHIN = 0.02f

/** The vehicle's drive along the line, which the driver is meant to follow: longer than any spring of the motion scheme. */
private val Drive = spring<Float>(dampingRatio = 0.85f, stiffness = 120f)

private const val FRAMES_BEFORE_DRIVE = 2

/** How fast the rider is lifted as the vehicle reaches them: with [AppMotion.defaultSpatialSpec], about 4 dp and back. */
private const val RIDER_LIFT = -268f

/** Where things stand along the line of a header [width] wide, from its start edge. */
internal class LineGeometry(private val width: Dp, private val stops: Int) {
    val narrow: Boolean = width < NarrowWidth
    val riderAt: Dp = width - RiderInset
    private val home: Dp = if (narrow) HomeInsetNarrow else HomeInset
    private val spacing: Dp = (riderAt - VehicleFromRider - home) / (stops + 1)

    /** [position] as [Readiness.vehicleAt] counts: 0 is before the first stop, n the nth stop. */
    fun at(position: Float): Dp = home + spacing * position

    /** Whether a check's icon fits over each stop. */
    val hasRoomForIcons: Boolean get() = spacing >= ClosestIcons
}

/** A part of the fold, as it fades or grows between [from] and [to] of it. */
internal fun within(fraction: Float, from: Float, to: Float): Float = ((fraction - from) / (to - from)).coerceIn(0f, 1f)

/**
 * The checks as stops on a route that ends at a rider, with the Tracking map's vehicle where it can get to: solid behind
 * it, dashed ahead. It is the lower edge of the header, [content] its header's content colour. [folded], 0 to 1, is how
 * far the header has folded and [iconsIn], 0 to 1, how much of the checks' icons shows; both are read only where the
 * line is drawn, and so is where the vehicle is, so nothing is recomposed for a frame of it. The header says the same in
 * words, so nothing here is read out.
 */
@Composable
internal fun ReadinessLine(readiness: Readiness, content: State<Color>, folded: () -> Float, iconsIn: State<Float>, modifier: Modifier = Modifier) {
    val colors = MaterialTheme.colorScheme
    val reached = colors.primary
    val ahead = colors.outline
    val inside = colors.surface
    val amber = AppTheme.problemColors.iconOnSurface
    val ring = if (readiness.canReport) AppTheme.statusColors.onTime.text else AppTheme.statusColors.neutral.text
    val towardsLeft = LocalLayoutDirection.current == LayoutDirection.Rtl
    val vehicle = painterResource(R.drawable.ic_map_vehicle_puck)
    val rider = painterResource(R.drawable.ic_emoji_people)
    val icons = PermissionRow.entries.map { painterResource(textOf(it).icon) }

    val at = remember { Animatable(readiness.vehicleAt.toFloat()) }
    val lift = remember { Animatable(0f) }
    LaunchedEffect(readiness.vehicleAt) {
        val drivesOn = readiness.canReport && at.value < readiness.vehicleAt
        // The header's words and colours change on the first frame, a long one on a slow phone. The drive starts after it.
        repeat(FRAMES_BEFORE_DRIVE) { withFrameNanos {} }
        at.animateTo(readiness.vehicleAt.toFloat(), Drive)
        if (drivesOn) lift.animateTo(0f, AppMotion.defaultSpatialSpec(), initialVelocity = RIDER_LIFT)
    }

    Spacer(
        modifier.drawWithCache {
            val geometry = LineGeometry(size.width.toDp(), readiness.stops.size)
            val y = size.height - LineThickness.toPx() / 2
            val dashes = PathEffect.dashPathEffect(floatArrayOf(8.dp.toPx(), 6.dp.toPx()))
            fun along(x: Dp): Float = if (towardsLeft) size.width - x.toPx() else x.toPx()
            val start = if (towardsLeft) size.width else 0f
            val end = size.width - start

            onDrawBehind {
                val vehicleAt = along(geometry.at(at.value))
                val arrived = readiness.canReport && at.value > readiness.vehicleAt - ARRIVED_WITHIN
                val fold = folded()

                if (!arrived) drawLine(ahead, Offset(vehicleAt, y), Offset(end, y), DashThickness.toPx(), pathEffect = dashes)
                drawLine(reached, Offset(start, y), Offset(if (arrived) end else vehicleAt, y), LineThickness.toPx())

                val iconAlpha = if (geometry.hasRoomForIcons) iconsIn.value * (1f - within(fold, 0.3f, 0.7f)) else 0f
                readiness.stops.forEachIndexed { index, stop ->
                    val centre = Offset(along(geometry.at(index + 1f)), y)
                    drawStop(stop, centre, reached, ahead, inside, amber)
                    if (iconAlpha > 0f) {
                        val tint = when (stop) {
                            StopState.ALLOWED -> content.value
                            StopState.MISSING -> amber
                            StopState.BEYOND -> ahead
                        }
                        drawIcon(icons[index], centre - Offset(0f, IconCentreAboveLine.toPx()), IconSize, tint, iconAlpha)
                    }
                }

                // Its feet on the line.
                val riderCentre = Offset(along(geometry.riderAt), y - (RiderSize / 2 + 2.dp).toPx() + lift.value.dp.toPx())
                drawIcon(rider, riderCentre, RiderSize, content.value, if (readiness.canReport) 1f else RIDER_FAR)

                val size = VehicleSize.toPx() * (if (geometry.narrow) NARROW_VEHICLE else 1f) * (1f - (1f - FOLDED_VEHICLE) * fold)
                drawVehicle(vehicle, Offset(vehicleAt, y), size, ring, towardsLeft)
            }
        },
    )
}

private fun DrawScope.drawStop(stop: StopState, centre: Offset, reached: Color, ahead: Color, inside: Color, amber: Color) {
    when (stop) {
        StopState.ALLOWED -> drawCircle(reached, 7.dp.toPx(), centre)
        StopState.MISSING -> {
            drawCircle(inside, 10.dp.toPx(), centre)
            drawCircle(amber, 8.dp.toPx(), centre, style = Stroke(4.dp.toPx()))
            // Barred as well as amber, so it does not rest on the colour alone.
            val bar = Size(8.dp.toPx(), 3.dp.toPx())
            drawRoundRect(amber, centre - Offset(bar.width / 2, bar.height / 2), bar, CornerRadius(bar.height / 2))
        }
        StopState.BEYOND -> {
            drawCircle(inside, 7.dp.toPx(), centre)
            drawCircle(ahead, 6.dp.toPx(), centre, style = Stroke(2.dp.toPx()))
        }
    }
}

private fun DrawScope.drawIcon(icon: Painter, centre: Offset, size: Dp, tint: Color, alpha: Float) {
    val side = size.toPx()
    translate(centre.x - side / 2, centre.y - side / 2) {
        with(icon) { draw(Size(side, side), alpha, ColorFilter.tint(tint)) }
    }
}

/** The Tracking map's marker, its ring under it. It points north, as the map turns it; here the way the line runs. */
private fun DrawScope.drawVehicle(vehicle: Painter, centre: Offset, side: Float, ring: Color, towardsLeft: Boolean) {
    drawCircle(ring, side / 2 * RING_OF_MARKER, centre)
    rotate(if (towardsLeft) -90f else 90f, centre) {
        translate(centre.x - side / 2, centre.y - side / 2) {
            with(vehicle) { draw(Size(side, side)) }
        }
    }
}

/** ic_map_vehicle_ring's radius in the marker's, 24 of 26. */
private const val RING_OF_MARKER = 24f / 26f
