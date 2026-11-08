package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.RunPage
import org.onebusaway.vehicletracker.ui.runs.TripRun
import org.onebusaway.vehicletracker.ui.runs.departureBoardsOf
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter

class DepartureBoardTest {
    private val zone = ZoneId.of("America/Los_Angeles")
    private val time = DateTimeFormatter.ofPattern("H:mm")

    private fun at(hour: Int, minute: Int): Instant = ZonedDateTime.of(2026, 10, 8, hour, minute, 0, 0, zone).toInstant()

    /** A ten-minute run, so no two of these overlap. */
    private fun run(id: String, hour: Int, minute: Int, direction: Int? = null, headsign: String = "Downtown") =
        TripRun(id, headsign, direction, "A", "B", at(hour, minute), at(hour, minute).plusSeconds(600), RunLabel("", ""))

    private fun boards(now: Instant, vararg runs: TripRun) = departureBoardsOf(RunPage("20261008", zone, runs.toList()), now, time)

    private val day = arrayOf(run("a", 6, 28), run("b", 6, 43), run("c", 6, 58), run("d", 7, 13))

    @Test fun `the board puts the line at now and opens on the run just gone`() {
        val board = boards(at(7, 0), *day).boards.single()
        assertNull("a route without directions has one board and no switch", board.direction)
        // Times on the agency's clock, formatted once, when the board is built.
        assertEquals(listOf("6:28", "6:43", "6:58", "7:13"), board.runs.map { it.time })
        assertEquals(listOf(true, true, false, false), board.runs.map { it.past })
        assertEquals(listOf(null, null, RunHighlight.NOW, null), board.runs.map { it.highlight })
        assertEquals(2, board.nowAt)
        assertEquals(1, board.openAt)
    }

    @Test fun `before the first run there is no line, and the board opens at the top`() {
        val board = boards(at(6, 0), *day).boards.single()
        assertEquals(RunHighlight.NEXT, board.runs.first().highlight)
        assertEquals(listOf(false, false, false, false), board.runs.map { it.past })
        assertNull(board.nowAt)
        assertEquals(0, board.openAt)
    }

    @Test fun `once every run has gone, the line comes after the last`() {
        val board = boards(at(9, 0), *day).boards.single()
        assertEquals(listOf(true, true, true, true), board.runs.map { it.past })
        assertEquals(listOf(null, null, null, null), board.runs.map { it.highlight })
        assertEquals(4, board.nowAt)
        assertEquals(3, board.openAt)
    }

    @Test fun `each direction has its own board with its own Now or Next`() {
        val boards = boards(
            at(7, 0),
            run("in1", 6, 28, direction = 0),
            run("out1", 6, 35, direction = 1, headsign = "Mount Baker"),
            run("in2", 6, 58, direction = 0),
            run("out2", 7, 20, direction = 1, headsign = "Mount Baker"),
        )
        assertEquals(listOf("Downtown", "Mount Baker"), boards.boards.map { it.direction?.headsign })
        assertEquals(listOf(listOf("in1", "in2"), listOf("out1", "out2")), boards.boards.map { board -> board.runs.map { it.id } })
        assertEquals(listOf(null, RunHighlight.NOW), boards.boards[0].runs.map { it.highlight })
        assertEquals(listOf(null, RunHighlight.NEXT), boards.boards[1].runs.map { it.highlight })
        // The route's run under way is inbound, so the screen opens on that board.
        assertEquals(0, boards.start)
    }
}
