package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.ui.runs.Board
import org.onebusaway.vehicletracker.ui.runs.BoardRun
import org.onebusaway.vehicletracker.ui.runs.RunDirection
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.RunPage
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.runNamed
import java.time.ZoneId

/** Which run the start dialog asks about, found on the boards as they are now. */
class RunNamedTest {
    private fun run(id: String, time: String) =
        BoardRun(id, time = time, headsign = "Downtown Seattle", label = RunLabel("", ""), past = false, highlight = null)

    private val outbound = Board(RunDirection(0, "Downtown Seattle", "A", "B"), listOf(run("T1", "6:43 AM")), nowAt = null, openAt = 0)
    private val inbound = Board(RunDirection(1, "Mount Baker", "B", "A"), listOf(run("T2", "6:58 AM"), run("T3", "7:13 AM")), nowAt = null, openAt = 0)

    private val loaded = RunsUiState.Loaded(
        page = RunPage("20261117", ZoneId.of("America/Los_Angeles"), emptyList()),
        boards = listOf(outbound, inbound),
        startBoard = 0,
        startingRunId = null,
        error = null,
    )

    @Test fun `a run on either board, as that board shows it`() {
        assertEquals(run("T1", "6:43 AM"), loaded.runNamed("T1"))
        assertEquals(run("T3", "7:13 AM"), loaded.runNamed("T3"))
    }

    @Test fun `none when no run is named`() {
        assertNull(loaded.runNamed(null))
    }

    @Test fun `none when the boards no longer have it, as after a reload on a new service day`() {
        assertNull(loaded.runNamed("T9"))
    }

    @Test fun `none while the runs are not loaded`() {
        assertNull(RunsUiState.Loading.runNamed("T1"))
        assertNull(RunsUiState.Error.runNamed("T1"))
        assertNull(RunsUiState.SessionEnded.runNamed("T1"))
    }
}
