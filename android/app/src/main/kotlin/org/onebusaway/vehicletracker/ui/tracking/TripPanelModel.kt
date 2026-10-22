package org.onebusaway.vehicletracker.ui.tracking

import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.map.MapFailure
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.map.downloadPercent
import org.onebusaway.vehicletracker.engine.TripStop

/** One row of the panel's stop list. */
internal sealed interface StopRow {
    data class Stop(val stop: TripStop, val kind: StopKind) : StopRow

    /** The vehicle itself, between the stop it last passed and the next one. */
    data object Vehicle : StopRow
}

internal enum class StopKind { PASSED, NEXT, UPCOMING, LAST }

/**
 * The panel's stop list: the stop the vehicle last passed, the vehicle, the next stop, and the rest to the
 * end of the line. Before the first fix, [nextStopIndex] is null and there is no vehicle to place: the list
 * is the whole run, from its first stop.
 */
internal fun stopRows(stops: List<TripStop>, nextStopIndex: Int?): List<StopRow> {
    if (stops.isEmpty()) return emptyList()
    val next = nextStopIndex?.takeIf { it in stops.indices } ?: return stops.mapIndexed { index, stop ->
        StopRow.Stop(stop, if (index == 0) StopKind.NEXT else upcoming(index, stops))
    }
    val rows = mutableListOf<StopRow>()
    if (next > 0) rows += StopRow.Stop(stops[next - 1], StopKind.PASSED)
    rows += StopRow.Vehicle
    rows += StopRow.Stop(stops[next], StopKind.NEXT)
    for (index in next + 1..stops.lastIndex) rows += StopRow.Stop(stops[index], upcoming(index, stops))
    return rows
}

private fun upcoming(index: Int, stops: List<TripStop>) = if (index == stops.lastIndex) StopKind.LAST else StopKind.UPCOMING

/** Whether the server is hearing from the phone, as the trip bar and the panel's Reports row say it. */
internal enum class Reporting {
    REPORTING,
    NOT_REPORTING,
    NO_GPS,
    STOPPED,
    ;

    val isProblem: Boolean get() = this != REPORTING
}

internal fun reporting(problem: TrackingProblem): Reporting = when (problem) {
    TrackingProblem.NONE -> Reporting.REPORTING
    // The server refuses the reports of a phone whose clock is wrong, so they reach no rider either.
    TrackingProblem.NO_NETWORK, TrackingProblem.CLOCK_SKEW, TrackingProblem.REPORTS_REFUSED -> Reporting.NOT_REPORTING
    TrackingProblem.NO_GPS -> Reporting.NO_GPS
    TrackingProblem.AUTH_EXPIRED -> Reporting.STOPPED
}

/** The panel's Street map row. The note over the map says more; this is the short form. */
internal sealed interface StreetMap {
    data object Ready : StreetMap
    data class Downloading(val percent: Int) : StreetMap
    data object NotDownloaded : StreetMap
    data object NoSpace : StreetMap
    data object Unusable : StreetMap
}

/** Null hides the row: with no street map, the agency has none or the server has not been asked yet. */
internal fun streetMap(state: MapFileState): StreetMap? = when (state) {
    MapFileState.None -> null
    is MapFileState.Downloading -> StreetMap.Downloading(downloadPercent(state.bytes, state.total))
    is MapFileState.Ready -> StreetMap.Ready
    is MapFileState.Failed -> when (state.reason) {
        MapFailure.NETWORK, MapFailure.UNEXPECTED -> StreetMap.NotDownloaded
        MapFailure.NO_SPACE -> StreetMap.NoSpace
        MapFailure.INVALID_FILE, MapFailure.CLEARTEXT_BLOCKED -> StreetMap.Unusable
    }
}

/**
 * How much of the panel under the trip bar shows, from 0 to 1. While the panel is down only the bar
 * shows: the rows under it would otherwise peek out behind the navigation bar. They come in over the
 * first [fadePx] of the pull, as the panel's top rises from [collapsedTop] to [sheetTop], which is
 * null before the panel has been placed.
 */
internal fun panelRowsAlpha(collapsedTop: Float, sheetTop: Float?, fadePx: Float): Float =
    if (sheetTop == null) 0f else ((collapsedTop - sheetTop) / fadePx.coerceAtLeast(1f)).coerceIn(0f, 1f)
