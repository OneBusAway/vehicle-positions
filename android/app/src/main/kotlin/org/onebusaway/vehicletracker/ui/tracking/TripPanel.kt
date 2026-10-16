package org.onebusaway.vehicletracker.ui.tracking

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.ui.map.MARKER_INK
import org.onebusaway.vehicletracker.ui.map.NEXT_STOP_FILL
import org.onebusaway.vehicletracker.ui.map.STOP_FILL
import org.onebusaway.vehicletracker.ui.map.STOP_RING
import org.onebusaway.vehicletracker.ui.map.casingColor
import org.onebusaway.vehicletracker.ui.map.routeLineColor
import org.onebusaway.vehicletracker.ui.routes.RouteBadge
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.tripTitle
import kotlin.math.roundToInt

private val SidePadding = 20.dp

/**
 * Narrower than this, End trip leaves out its icon so the trip's time and sent count still fit
 * beside it, as the design's 360 dp phone has it. A wider bar does the same once the count is cut
 * short, which a long route name, a large font or a long shift can do.
 */
private val CompactBarWidth = 380.dp

/**
 * What the trip bar pulls up into. The bar stays its top row, so End trip and the sent count never move;
 * under it are the run, how reporting is going and the street map, then the stops from the one last passed
 * to the end of the line. [onBarHeightChanged] hands back the bar's height, which is what the panel shows
 * while it is down, and [rowsAlpha] how much of the rest shows, read as it is placed and drawn. The panel
 * runs under the navigation bar, [navigationBarPx] tall, where the rows would otherwise sit while it is
 * down: they are held that far below the screen instead, and rise and fade in with the pull.
 */
@Composable
internal fun TripPanel(
    state: TrackingUiState,
    model: StatusCardModel,
    onEndTripClick: () -> Unit,
    onBarClick: () -> Unit,
    onBarHeightChanged: (Int) -> Unit,
    rowsAlpha: () -> Float,
    navigationBarPx: Int,
    modifier: Modifier = Modifier,
) {
    val geometry = state.tracking.geometry
    val reporting = reporting(state.tracking.problem)
    Column(modifier) {
        TripBar(
            geometry = geometry,
            startedAtEpochSec = state.tracking.tripStartedAtEpochSec ?: state.activeTrip?.startedAtEpochSec,
            fixesSent = state.tracking.fixesSent,
            reporting = reporting,
            ending = state.ending,
            onEndTripClick = onEndTripClick,
            onClick = onBarClick,
            modifier = Modifier.onSizeChanged { onBarHeightChanged(it.height) },
        )
        Column(
            Modifier
                .weight(1f, fill = false)
                .offset { IntOffset(0, (navigationBarPx * (1f - rowsAlpha())).roundToInt()) }
                .graphicsLayer { alpha = rowsAlpha() },
        ) {
            val firstStop = geometry?.stops?.firstOrNull()
            if (geometry != null && firstStop != null) {
                val startsAt = formatClock(firstStop.departureAt, geometry.timezone)
                FactRow(
                    icon = R.drawable.ic_directions_bus,
                    label = stringResource(R.string.tracking_fact_run),
                    value = state.activeTrip?.vehicleId?.let { stringResource(R.string.tracking_fact_run_vehicle, startsAt, it) } ?: startsAt,
                )
            }
            FactRow(
                icon = reportingIcon(reporting),
                label = stringResource(R.string.tracking_fact_reports),
                value = stringResource(reportingText(reporting)),
                problem = reporting.isProblem,
            )
            streetMap(state.mapFile)?.let { map ->
                FactRow(icon = R.drawable.ic_map, label = stringResource(R.string.tracking_fact_street_map), value = streetMapText(map))
            }
            if (geometry != null) {
                HorizontalDivider(Modifier.padding(start = SidePadding, end = SidePadding, top = 6.dp, bottom = 8.dp))
                val adherence = state.tracking.adherence
                StopList(
                    geometry = geometry,
                    rows = stopRows(geometry.stops, adherence?.let { geometry.stops.indexOf(it.nextStop) }),
                    vehicleId = state.activeTrip?.vehicleId.orEmpty(),
                    model = model,
                    modifier = Modifier.weight(1f, fill = false),
                )
            }
            // The last stop scrolls clear of the navigation bar.
            Spacer(Modifier.height(with(LocalDensity.current) { navigationBarPx.toDp() }))
        }
    }
}

/** Route, headsign, the trip's time and sent count, and End trip, which never leaves the screen. */
@Composable
private fun TripBar(
    geometry: TripGeometry?,
    startedAtEpochSec: Long?,
    fixesSent: Int,
    reporting: Reporting,
    ending: Boolean,
    onEndTripClick: () -> Unit,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    BoxWithConstraints(modifier.fillMaxWidth()) {
        // Once set it stays set: without the icon the count fits, and the icon must not come back.
        var countCutShort by remember { mutableStateOf(false) }
        val compact = maxWidth < CompactBarWidth || countCutShort
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClickLabel = stringResource(R.string.tracking_bar_click_label), onClick = onClick)
                .padding(start = SidePadding, end = 16.dp, bottom = 12.dp),
            horizontalArrangement = Arrangement.spacedBy(14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RouteBadge(
                shortName = geometry?.routeShortName.orEmpty(),
                color = geometry?.routeColor.orEmpty(),
                textColor = geometry?.routeTextColor.orEmpty(),
                modifier = Modifier.sizeIn(minWidth = 48.dp, minHeight = 48.dp),
                style = MaterialTheme.typography.headlineSmall,
                shape = MaterialTheme.shapes.medium,
            )
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    text = geometry?.headsign.orEmpty(),
                    style = MaterialTheme.typography.tripTitle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
                    TripDuration(startedAtEpochSec, MaterialTheme.typography.bodyMedium, quiet)
                    Text(
                        text = stringResource(R.string.tracking_separator),
                        style = MaterialTheme.typography.bodyMedium,
                        color = quiet,
                        modifier = Modifier.clearAndSetSemantics {},
                    )
                    Icon(
                        painter = painterResource(reportingIcon(reporting)),
                        contentDescription = null,
                        tint = if (reporting.isProblem) AppTheme.problemColors.iconOnSurface else quiet,
                        modifier = Modifier.size(18.dp),
                    )
                    Text(
                        text = pluralStringResource(R.plurals.tracking_bar_sent, fixesSent, fixesSent),
                        style = if (reporting.isProblem) MaterialTheme.typography.titleSmall else MaterialTheme.typography.bodyMedium,
                        color = if (reporting.isProblem) MaterialTheme.colorScheme.onSurface else quiet,
                        maxLines = 1,
                        // Squeezed, the count wraps after the number, and its one line shows only that.
                        onTextLayout = { if (it.didOverflowHeight) countCutShort = true },
                    )
                }
            }
            EndTripButton(ending, compact, onEndTripClick)
        }
    }
}

/**
 * Outlined and ink, not red: red means early on this screen. It says so on itself while the end is
 * on its way. On a [compact] bar it leaves out its icon, though not the spinner while ending.
 */
@Composable
private fun EndTripButton(ending: Boolean, compact: Boolean, onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    OutlinedButton(
        onClick = onClick,
        enabled = !ending,
        modifier = Modifier.heightIn(min = 56.dp),
        contentPadding = PaddingValues(horizontal = if (compact) 16.dp else 20.dp),
        border = BorderStroke(1.5.dp, if (ending) colors.outlineVariant else colors.outline),
        colors = ButtonDefaults.outlinedButtonColors(contentColor = colors.onSurface, disabledContentColor = colors.onSurfaceVariant),
    ) {
        if (ending) {
            CircularProgressIndicator(modifier = Modifier.size(20.dp), color = colors.onSurfaceVariant, strokeWidth = 2.dp)
            Spacer(Modifier.width(8.dp))
        } else if (!compact) {
            Icon(painterResource(R.drawable.ic_stop_circle), contentDescription = null, modifier = Modifier.size(22.dp))
            Spacer(Modifier.width(8.dp))
        }
        Text(
            text = stringResource(if (ending) R.string.tracking_end_trip_ending else R.string.tracking_end_trip_button),
            style = MaterialTheme.typography.titleMedium,
        )
    }
}

private fun reportingIcon(reporting: Reporting): Int = when (reporting) {
    Reporting.REPORTING -> R.drawable.ic_cloud_done
    Reporting.NOT_REPORTING -> R.drawable.ic_cloud_off
    Reporting.NO_GPS -> R.drawable.ic_location_disabled
    Reporting.STOPPED -> R.drawable.ic_lock
}

private fun reportingText(reporting: Reporting): Int = when (reporting) {
    Reporting.REPORTING -> R.string.tracking_reports_reporting
    Reporting.NOT_REPORTING -> R.string.tracking_reports_not_reporting
    Reporting.NO_GPS -> R.string.tracking_reports_no_gps
    Reporting.STOPPED -> R.string.tracking_reports_stopped
}

@Composable
private fun streetMapText(map: StreetMap): String = when (map) {
    StreetMap.Ready -> stringResource(R.string.tracking_street_map_ready)
    is StreetMap.Downloading -> stringResource(R.string.tracking_street_map_downloading, map.percent)
    StreetMap.NotDownloaded -> stringResource(R.string.tracking_street_map_not_downloaded)
    StreetMap.NoSpace -> stringResource(R.string.tracking_street_map_no_space)
    StreetMap.Unusable -> stringResource(R.string.tracking_street_map_unusable)
}

/** One fact about the trip: an icon, what it is, and its value. A problem shows in an amber icon and heavier text. */
@Composable
private fun FactRow(icon: Int, label: String, value: String, problem: Boolean = false) {
    val colors = MaterialTheme.colorScheme
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 44.dp)
            .padding(horizontal = SidePadding)
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            painter = painterResource(icon),
            contentDescription = null,
            tint = if (problem) AppTheme.problemColors.iconOnSurface else colors.onSurfaceVariant,
            modifier = Modifier.size(22.dp),
        )
        Text(label, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
        Text(
            text = value,
            style = if (problem) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyLarge,
            color = if (problem) colors.onSurface else colors.onSurfaceVariant,
        )
    }
}

/**
 * The stops along a strip of the route, as on the map: the vehicle between the stop it last passed and the
 * next one, which is orange, and the last stop in an ink ring.
 */
@Composable
private fun StopList(geometry: TripGeometry, rows: List<StopRow>, vehicleId: String, model: StatusCardModel, modifier: Modifier) {
    // As the map draws it, in the same theme.
    val dark = isSystemInDarkTheme()
    val routeColor = routeLineColor(geometry.routeColor, dark)
    val line = Color(routeColor)
    val casing = Color(casingColor(routeColor, dark))
    LazyColumn(modifier = modifier, contentPadding = PaddingValues(horizontal = SidePadding)) {
        itemsIndexed(rows) { index, row ->
            val strip = RouteStrip(line, casing, last = index == rows.lastIndex)
            when (row) {
                is StopRow.Stop -> StopListRow(
                    name = row.stop.name,
                    time = formatClock(row.stop.arrivalAt, geometry.timezone),
                    kind = row.kind,
                    strip = strip,
                )
                StopRow.Vehicle -> VehicleRow(vehicleId, model, strip)
            }
        }
    }
}

/** The route as the stop list draws it down its left edge: the line in its colour between two casing edges. */
private class RouteStrip(val line: Color, val casing: Color, val last: Boolean) {
    /** The strip through a row, ending at the row's middle on the last one so the line stops at the end of the line. */
    fun draw(scope: DrawScope) = with(scope) {
        val left = 8.dp.toPx()
        val width = 12.dp.toPx()
        val edge = 2.dp.toPx()
        val bottom = if (last) size.height / 2 else size.height
        drawRect(casing, Offset(left, 0f), Size(width, bottom))
        drawRect(line, Offset(left + edge, 0f), Size(width - 2 * edge, bottom))
    }
}

private val MarkerColumnWidth = 28.dp

@Composable
private fun StopListRow(name: String, time: String, kind: StopKind, strip: RouteStrip) {
    val next = kind == StopKind.NEXT
    val style = if (next) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyLarge
    Row(
        modifier = Modifier.fillMaxWidth().height(46.dp).semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier.width(MarkerColumnWidth).fillMaxHeight().drawBehind {
                strip.draw(this)
                val centre = Offset(14.dp.toPx(), size.height / 2)
                when (kind) {
                    StopKind.NEXT -> circles(centre, 11.dp to Color(STOP_FILL), 9.dp to Color(MARKER_INK), 6.dp to Color(NEXT_STOP_FILL))
                    StopKind.LAST -> circles(centre, 7.dp to Color(MARKER_INK), 4.dp to Color(STOP_FILL))
                    StopKind.PASSED, StopKind.UPCOMING -> circles(centre, 6.dp to Color(STOP_RING), 4.dp to Color(STOP_FILL))
                }
            },
        )
        Text(name, style = style, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
        Text(time, style = style, color = if (next) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** The vehicle's row: its id, and how it is keeping to the schedule in the status colour. */
@Composable
private fun VehicleRow(vehicleId: String, model: StatusCardModel, strip: RouteStrip) {
    val ring = AppTheme.statusColors[model.tone].text
    val halo = MaterialTheme.colorScheme.surfaceContainerLow
    val inner = MaterialTheme.colorScheme.onSurface
    Row(
        modifier = Modifier.fillMaxWidth().height(40.dp).semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier.width(MarkerColumnWidth).fillMaxHeight().drawBehind {
                strip.draw(this)
                circles(Offset(14.dp.toPx(), size.height / 2), 14.dp to halo, 12.dp to ring, 8.dp to inner)
            },
        )
        Text(vehicleId, style = MaterialTheme.typography.titleMedium, maxLines = 1, modifier = Modifier.weight(1f))
        Text(statusShort(model), style = MaterialTheme.typography.titleMedium, color = ring)
    }
}

/** How the vehicle is keeping to the schedule, in a few words: the card's answer without a distance. */
@Composable
private fun statusShort(model: StatusCardModel): String = when (model) {
    is StatusCardModel.Adherence -> deviationText(model.deviationSec)
    is StatusCardModel.OffSchedule -> deviationText(model.deviationSec)
    is StatusCardModel.OffRoute -> stringResource(R.string.tracking_adherence_off_route_short)
    // No fix, so no vehicle row to put this in.
    is StatusCardModel.WaitingForGps, StatusCardModel.NoSchedule -> ""
}

/** Circles about [centre] in the order given, largest first, so each ring shows round the one inside it. */
private fun DrawScope.circles(centre: Offset, vararg layers: Pair<Dp, Color>) {
    for ((radius, color) in layers) drawCircle(color, radius.toPx(), centre)
}
