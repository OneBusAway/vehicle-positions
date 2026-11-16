package org.onebusaway.vehicletracker.ui.welcome

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import kotlin.math.min

private val LineHeight = 28.dp
private val LineWidth = 150.dp
private val LineWidthCompact = 120.dp
private val MarkerSize = 26.dp

// A stop counts as reached a little before the vehicle is dead on it, so its spring's last settling doesn't flicker it.
private const val REACHED_WITHIN = 0.01f

/**
 * The welcome's progress, as a short route: a stop for each screen, and the Tracking map's vehicle on the one the
 * driver is at. [position] is where the vehicle is along it, 0 to 1, and is read only where the line is drawn, so the
 * vehicle slides to its stop without recomposing anything. TalkBack reads the whole line as the [screen]'s step.
 */
@Composable
internal fun StepLine(screen: Int, position: State<Float>, compact: Boolean, modifier: Modifier = Modifier) {
    val colors = MaterialTheme.colorScheme
    val track = colors.surfaceContainerHigh
    val reached = colors.primary
    val ahead = colors.surface
    val aheadRing = colors.outline
    val vehicle = painterResource(R.drawable.ic_map_vehicle_puck)
    val towardsLeft = LocalLayoutDirection.current == LayoutDirection.Rtl
    val step = stringResource(R.string.welcome_step, screen + 1, WELCOME_SCREENS)

    Spacer(
        modifier
            .height(LineHeight)
            .semantics { contentDescription = step }
            .drawWithCache {
                val marker = MarkerSize.toPx()
                // The vehicle overhangs the line's ends, so the line gives up that much of a narrow row.
                val length = min((if (compact) LineWidthCompact else LineWidth).toPx(), size.width - marker)
                val from = (size.width - length) / 2 + if (towardsLeft) length else 0f
                val along = if (towardsLeft) -length else length
                val y = size.height / 2
                val thickness = 6.dp.toPx()
                val stop = 6.dp.toPx()
                val ring = Stroke(2.dp.toPx())

                onDrawBehind {
                    val at = position.value
                    val vehicleAt = Offset(from + along * at, y)
                    drawLine(track, Offset(from, y), Offset(from + along, y), thickness, StrokeCap.Round)
                    drawLine(reached, Offset(from, y), vehicleAt, thickness, StrokeCap.Round)
                    for (index in 0 until WELCOME_SCREENS) {
                        val fraction = index / (WELCOME_SCREENS - 1f)
                        val center = Offset(from + along * fraction, y)
                        if (at >= fraction - REACHED_WITHIN) {
                            drawCircle(reached, stop, center)
                        } else {
                            drawCircle(ahead, stop, center)
                            drawCircle(aheadRing, stop - ring.width / 2, center, style = ring)
                        }
                    }
                    // The marker points north, as the map turns it; here it points the way the line runs.
                    drawCircle(ahead, marker / 2, vehicleAt)
                    rotate(if (towardsLeft) -90f else 90f, vehicleAt) {
                        translate(vehicleAt.x - marker / 2, vehicleAt.y - marker / 2) {
                            with(vehicle) { draw(Size(marker, marker)) }
                        }
                    }
                }
            },
    )
}
