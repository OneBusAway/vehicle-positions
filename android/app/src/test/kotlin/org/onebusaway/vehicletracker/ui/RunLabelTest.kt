package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.RouteTripsDto
import org.onebusaway.vehicletracker.data.api.TripSummaryDto
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.departureBoardsOf
import org.onebusaway.vehicletracker.ui.runs.runLabelText
import org.onebusaway.vehicletracker.ui.runs.toRunPage
import java.time.Instant
import java.time.format.DateTimeFormatter

class RunLabelTest {
    private fun text(label: RunLabel) =
        runLabelText(label, number = { "Run $it" }, block = { "Block $it" }, both = { number, block -> "$number / $block" })

    @Test fun `a run with a number and a block shows both`() {
        assertEquals("Run 1503 / Block 599-20", text(RunLabel(number = "1503", block = "599-20")))
    }

    @Test fun `a run with only a number shows the number`() {
        assertEquals("Run 1503", text(RunLabel(number = "1503", block = "")))
    }

    @Test fun `a run with only a block shows the block`() {
        assertEquals("Block 599-20", text(RunLabel(number = "", block = "599-20")))
    }

    @Test fun `a run with neither gets no line`() {
        assertNull(text(RunLabel(number = "", block = "")))
    }

    @Test fun `a run's number and block reach its row on the board`() {
        val trip = TripSummaryDto(
            id = "t1",
            headsign = "Angle Lake",
            directionId = 0,
            startsAt = "2026-10-26T07:00:00-07:00",
            endsAt = "2026-10-26T07:40:00-07:00",
            firstStop = "Lynnwood City Center",
            lastStop = "Angle Lake",
            shortName = "1503",
            blockId = "599-20",
        )
        val page = RouteTripsDto("100479", "20261026", "America/Los_Angeles", listOf(trip)).toRunPage()

        val row = departureBoardsOf(page, Instant.parse("2026-10-26T13:00:00Z"), DateTimeFormatter.ofPattern("H:mm"))
            .boards.single().runs.single()

        assertEquals(RunLabel(number = "1503", block = "599-20"), row.label)
    }
}
