@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.settings

import androidx.annotation.StringRes
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.Placeable
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.util.lerp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.Fix
import org.onebusaway.vehicletracker.ui.permissions.Marker
import org.onebusaway.vehicletracker.ui.permissions.MarkerSize
import org.onebusaway.vehicletracker.ui.permissions.PermissionFixes
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.crossFade
import org.onebusaway.vehicletracker.ui.permissions.fixOf
import org.onebusaway.vehicletracker.ui.permissions.LocationNoticeHost
import org.onebusaway.vehicletracker.ui.permissions.rememberLocationNotice
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFixes
import org.onebusaway.vehicletracker.ui.permissions.rememberRowTick
import org.onebusaway.vehicletracker.ui.permissions.textOf
import org.onebusaway.vehicletracker.ui.theme.AppTheme

private val SideInset = 24.dp
private val RowMinHeight = 88.dp
private val RowPadding = 8.dp
private val MarkerToWords = 16.dp
private val WordsToEnd = 12.dp
private val WordsToButton = 8.dp

/** Less room than this for a row's words, and its button goes under them. */
private val LeastWords = 170.dp

private val VehicleSize = 34.dp

/** Over the first row, where the vehicle waits when it can't get past it. */
private val HomeHeight = 44.dp

/**
 * Each thing the app needs to report a trip, whether the phone allows it, and what fixes one it doesn't: the line of
 * Settings' header, upright. It starts no trip, and asks for nothing but the row that is tapped.
 */
@Composable
fun PermissionCheckScreen(onBack: () -> Unit) {
    val access = rememberPhoneAccess()
    val notice = rememberLocationNotice()
    val fixes = rememberPermissionFixes(onAnswer = access::refresh, notice = notice)
    // A location row's Allow, before Android asks.
    LocationNoticeHost(fixes.noticeFor != null, notice.server, onAgree = fixes::agreeToNotice, onNotNow = fixes::declineNotice) {
        PermissionCheckContent(readiness(access.value, waiting = null), fixes, onBack)
    }
}

@Composable
internal fun PermissionCheckContent(readiness: Readiness, fixes: PermissionFixes, onBack: () -> Unit) {
    // Pinned: the bar stays put and takes the container colour once the page is under it.
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        TopAppBar(
            title = { Text(stringResource(R.string.permission_check_title), modifier = Modifier.semantics { heading() }) },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
                }
            },
            scrollBehavior = scrollBehavior,
        )
        Column(Modifier.verticalScroll(rememberScrollState()).padding(bottom = 32.dp)) {
            Text(
                text = stringResource(R.string.permission_check_intro),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = SideInset),
            )
            CheckLine(readiness) {
                for ((row, stop) in PermissionRow.entries.zip(readiness.stops)) {
                    CheckRow(row, stop, fixOf(row, fixes.onlyInSettings), onFix = { fixes.fix(row) })
                }
                RidersRow(readiness.canReport)
            }
        }
    }
}

/**
 * The [rows] under each other with the line through their markers: solid to the vehicle, dashed after it. The vehicle
 * waits over the row it can't get past, or comes to the riders. Where it is, is read only where it is drawn. The rows say
 * the same, so nothing here is read out.
 */
@Composable
private fun CheckLine(readiness: Readiness, rows: @Composable () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val reached = colors.primary
    val ahead = colors.outline
    val ring = if (readiness.canReport) AppTheme.statusColors.onTime.text else AppTheme.statusColors.neutral.text
    val vehicle = painterResource(R.drawable.ic_map_vehicle_puck)
    val towardsLeft = LocalLayoutDirection.current == LayoutDirection.Rtl

    // Past the header line's last stop the vehicle is by the riders, which here is over their row.
    val waitsOver = readiness.vehicleAt.coerceAtMost(readiness.stops.size)
    val at = remember { Animatable(waitsOver.toFloat()) }
    LaunchedEffect(waitsOver) { at.driveTo(waitsOver.toFloat()) }
    // Where the vehicle can wait, from the top: over each row, the riders' last. Then the middle of the riders' marker.
    var places by remember { mutableStateOf(emptyList<Int>()) }

    Layout(
        content = rows,
        modifier = Modifier.fillMaxWidth().drawWithContent {
            val placed = places
            if (placed.size < 2) {
                drawContent()
                return@drawWithContent
            }
            val inset = (SideInset + MarkerSize / 2).toPx()
            val x = if (towardsLeft) size.width - inset else inset
            val end = placed.last().toFloat()
            val over = at.value.coerceIn(0f, placed.size - 2f)
            val vehicleAt = lerp(placed[over.toInt()].toFloat(), placed[(over.toInt() + 1).coerceAtMost(placed.size - 2)].toFloat(), over % 1f)
            val arrived = readiness.canReport && over > placed.size - 2 - ARRIVED_WITHIN
            if (!arrived) drawLine(ahead, Offset(x, vehicleAt), Offset(x, end), DashThickness.toPx(), pathEffect = lineDashes())
            drawLine(reached, Offset(x, 0f), Offset(x, if (arrived) end else vehicleAt), LineThickness.toPx())
            drawContent()
            drawVehicle(vehicle, Offset(x, vehicleAt), VehicleSize.toPx(), ring, pointing = 180f)
        },
    ) { measurables, constraints ->
        val placeables = measurables.map { it.measure(Constraints.fixedWidth(constraints.maxWidth)) }
        val home = HomeHeight.roundToPx()
        val tops = placeables.runningFold(home) { top, row -> top + row.height }
        places = listOf(home - VehicleSize.roundToPx() / 2 - 2.dp.roundToPx()) + tops.subList(1, placeables.size) +
            (tops[placeables.lastIndex] + placeables.last().height / 2)
        layout(constraints.maxWidth, tops.last()) {
            placeables.forEachIndexed { index, row -> row.place(0, tops[index]) }
        }
    }
}

/** How a check stands: its marker, ticking as the screen sees it allowed, its words, and its state or what fixes it. */
@Composable
private fun CheckRow(row: PermissionRow, stop: StopState, fix: Fix, onFix: () -> Unit) {
    val text = textOf(row)
    val tick = rememberRowTick(allowed = stop == StopState.ALLOWED)
    val title = stringResource(text.title)
    val allowed = stringResource(R.string.preflight_row_state, title, stringResource(R.string.preflight_allowed))
    RowWithEnd(
        modifier = Modifier.graphicsLayer { translationY = tick.dip.toPx() },
        marker = {
            AnimatedContent(markerOf(stop), transitionSpec = { tick.markerChange() }, label = "marker") { Marker(it, text.icon) }
        },
        words = { CheckWords(title, stringResource(if (stop == StopState.MISSING) missingWhyOf(row) ?: text.why else text.why)) },
        end = {
            AnimatedContent(
                targetState = stop to fix,
                // As Before you start's: one node says the state, with the row's name, so TalkBack reads a change once.
                modifier = if (stop == StopState.MISSING) {
                    Modifier
                } else {
                    Modifier.clearAndSetSemantics {
                        if (stop == StopState.ALLOWED) contentDescription = allowed
                        liveRegion = LiveRegionMode.Polite
                    }
                },
                transitionSpec = { crossFade() using null },
                label = "state",
            ) { (state, offered) ->
                when (state) {
                    StopState.ALLOWED -> Text(
                        text = stringResource(R.string.preflight_allowed),
                        style = MaterialTheme.typography.labelLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    StopState.MISSING -> FixButton(title, offered, onFix)
                    StopState.BEYOND -> Unit
                }
            }
        },
    )
}

private fun markerOf(stop: StopState): Marker = when (stop) {
    StopState.ALLOWED -> Marker.DONE
    StopState.MISSING -> Marker.SKIPPED
    StopState.BEYOND -> Marker.PENDING
}

/** What a missing row says in place of its why, or null to keep it. */
@StringRes
private fun missingWhyOf(row: PermissionRow): Int? = when (row) {
    PermissionRow.LOCATION -> R.string.preflight_location_needed
    PermissionRow.PRECISE -> R.string.preflight_approximate
    PermissionRow.BACKGROUND, PermissionRow.NOTIFICATIONS, PermissionRow.BATTERY -> R.string.preflight_skipped_why
    PermissionRow.LOCATION_SERVICES -> null
}

@StringRes
private fun nameOf(fix: Fix): Int = when (fix) {
    Fix.ALLOW -> R.string.permission_check_allow
    Fix.OPEN_SETTINGS -> R.string.preflight_open_settings
    Fix.TURN_ON -> R.string.permission_check_turn_on
}

/** TalkBack reads it with the row's name: the page can have several that say Allow. */
@Composable
private fun FixButton(row: String, fix: Fix, onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val said = stringResource(R.string.preflight_row_state, row, stringResource(nameOf(fix)))
    Button(
        onClick = onClick,
        colors = ButtonDefaults.buttonColors(containerColor = colors.primaryContainer, contentColor = colors.onPrimaryContainer),
        contentPadding = PaddingValues(horizontal = 20.dp, vertical = 8.dp),
        modifier = Modifier.heightIn(min = 48.dp).semantics { contentDescription = said },
    ) {
        Text(stringResource(nameOf(fix)))
    }
}

/** Where the line ends: whether riders can see the vehicle. */
@Composable
private fun RidersRow(canReport: Boolean) {
    val colors = MaterialTheme.colorScheme
    RowWithEnd(
        marker = {
            if (canReport) {
                Box(Modifier.size(MarkerSize).background(colors.primaryContainer, CircleShape), contentAlignment = Alignment.Center) {
                    Icon(painterResource(R.drawable.ic_emoji_people), contentDescription = null, tint = colors.onPrimaryContainer, modifier = Modifier.size(24.dp))
                }
            } else {
                Marker(Marker.PENDING, R.drawable.ic_emoji_people)
            }
        },
        words = {
            CheckWords(
                stringResource(R.string.permission_check_riders),
                stringResource(if (canReport) R.string.permission_check_riders_ready else R.string.permission_check_riders_not_ready),
            )
        },
        end = {},
    )
}

@Composable
private fun CheckWords(title: String, why: String) {
    Column(Modifier.semantics(mergeDescendants = true) {}, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        Text(why, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** A row's [marker], [words] and [end], which goes under the words where they would have less than [LeastWords]. */
@Composable
private fun RowWithEnd(
    marker: @Composable () -> Unit,
    words: @Composable () -> Unit,
    end: @Composable () -> Unit,
    modifier: Modifier = Modifier,
) {
    Layout(
        content = {
            Box { marker() }
            Box { words() }
            Box { end() }
        },
        modifier = modifier.fillMaxWidth().padding(horizontal = SideInset),
    ) { (markerMeasurable, wordsMeasurable, endMeasurable), constraints ->
        val loose = Constraints(maxWidth = constraints.maxWidth)
        val mark = markerMeasurable.measure(loose)
        val wordsX = mark.width + MarkerToWords.roundToPx()
        val room = constraints.maxWidth - wordsX
        val ending = endMeasurable.measure(Constraints(maxWidth = room))
        val beside = room - if (ending.width > 0) ending.width + WordsToEnd.roundToPx() else 0
        val stacks = beside < LeastWords.roundToPx()
        val said = wordsMeasurable.measure(Constraints(maxWidth = if (stacks) room else beside))

        val content = if (stacks) said.height + WordsToButton.roundToPx() + ending.height else maxOf(said.height, ending.height)
        val height = maxOf(RowMinHeight.roundToPx(), maxOf(content, mark.height) + 2 * RowPadding.roundToPx())
        fun middle(placeable: Placeable) = (height - placeable.height) / 2
        layout(constraints.maxWidth, height) {
            mark.placeRelative(0, middle(mark))
            if (stacks) {
                val top = (height - content) / 2
                said.placeRelative(wordsX, top)
                ending.placeRelative(wordsX, top + content - ending.height)
            } else {
                said.placeRelative(wordsX, middle(said))
                ending.placeRelative(constraints.maxWidth - ending.width, middle(ending))
            }
        }
    }
}
