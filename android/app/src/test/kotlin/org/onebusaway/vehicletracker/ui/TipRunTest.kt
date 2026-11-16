package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.ui.runs.Board
import org.onebusaway.vehicletracker.ui.runs.BoardRun
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.tipRunOf

/** Which run the run board's tip points at. */
class TipRunTest {
    private fun run(id: String, past: Boolean = false, highlight: RunHighlight? = null) =
        BoardRun(id, time = "7:13 AM", headsign = "Downtown Seattle", label = RunLabel("", ""), past = past, highlight = highlight)

    private fun board(vararg runs: BoardRun) = Board(direction = null, runs = runs.toList(), nowAt = null, openAt = 0)

    @Test fun `the run marked Now`() {
        assertEquals("T2", tipRunOf(board(run("T1", past = true), run("T2", highlight = RunHighlight.NOW), run("T3"))))
    }

    @Test fun `on a day none has started, the first, which is the one marked Next`() {
        assertEquals("T1", tipRunOf(board(run("T1", highlight = RunHighlight.NEXT), run("T2"))))
    }

    @Test fun `between runs, the one marked Next rather than one that has gone`() {
        assertEquals("T3", tipRunOf(board(run("T1", past = true), run("T2", past = true), run("T3", highlight = RunHighlight.NEXT))))
    }

    @Test fun `with every run gone and none marked, the first`() {
        assertEquals("T1", tipRunOf(board(run("T1", past = true), run("T2", past = true))))
    }

    @Test fun `none on a board with no runs`() {
        assertNull(tipRunOf(board()))
    }
}
