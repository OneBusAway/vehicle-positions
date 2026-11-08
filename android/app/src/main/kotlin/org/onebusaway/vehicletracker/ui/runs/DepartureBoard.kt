package org.onebusaway.vehicletracker.ui.runs

import androidx.compose.runtime.Immutable
import java.time.Instant
import java.time.format.DateTimeFormatter

/** One run as the departure board draws it, its start already on the agency's clock in [time]. */
@Immutable
data class BoardRun(
    val id: String,
    val time: String,
    val headsign: String,
    val label: RunLabel,
    val past: Boolean,
    val highlight: RunHighlight?,
)

/**
 * One direction's departures, or all of a route's when the runs do not split into two directions; [direction] is
 * null for that single list, which has no switch over it. [nowAt] is where the Now line goes, before the run at that
 * index (after the last once every run has gone); null when no run has gone yet. [openAt] is the item the board
 * opens on: the run just gone, so the Now line and the run under way or next sit under it.
 */
@Immutable
data class Board(val direction: RunDirection?, val runs: List<BoardRun>, val nowAt: Int?, val openAt: Int)

/** A route's boards, and the index of the one the screen opens on. */
data class DepartureBoards(val boards: List<Board>, val start: Int)

/**
 * The departure boards for [page] at [now]. Each board has its own Now or Next run, so switching direction shows
 * where that direction's day is. The screen opens on the direction of the route's Now or Next run.
 */
fun departureBoardsOf(page: RunPage, now: Instant, timeFormat: DateTimeFormatter): DepartureBoards {
    val directions = directionsOf(page.runs, highlightedRun(page.runs, now)?.id)
        ?: return DepartureBoards(listOf(boardOf(null, page.runs, page, now, timeFormat)), start = 0)
    val boards = directions.directions.map { direction ->
        boardOf(direction, page.runs.filter { it.directionId == direction.id }, page, now, timeFormat)
    }
    return DepartureBoards(boards, directions.start)
}

private fun boardOf(direction: RunDirection?, runs: List<TripRun>, page: RunPage, now: Instant, timeFormat: DateTimeFormatter): Board {
    val highlighted = highlightedRun(runs, now)
    // The runs come in first-departure order, so every run before the highlighted one has gone.
    val gone = if (highlighted == null) runs.size else runs.indexOf(highlighted)
    return Board(
        direction = direction,
        runs = runs.mapIndexed { index, run ->
            BoardRun(
                id = run.id,
                time = timeFormat.format(clockTime(run.startsAt, page.timezone)),
                headsign = run.headsign,
                label = run.label,
                past = index < gone,
                highlight = if (run == highlighted) highlightOf(run, now) else null,
            )
        },
        nowAt = gone.takeIf { it > 0 },
        openAt = (gone - 1).coerceAtLeast(0),
    )
}
