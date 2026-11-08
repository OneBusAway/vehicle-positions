package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.RouteTripsDto
import org.onebusaway.vehicletracker.data.api.TripSummaryDto
import org.onebusaway.vehicletracker.ui.runs.RunDirection
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.TripRun
import org.onebusaway.vehicletracker.ui.runs.directionsOf
import org.onebusaway.vehicletracker.ui.runs.toRunPage
import java.time.Instant

class RunDirectionsTest {
    private val start = Instant.parse("2026-10-08T14:00:00Z")

    private fun run(id: String, directionId: Int?, headsign: String, firstStop: String = "A", lastStop: String = "B") =
        TripRun(id, headsign, directionId, firstStop, lastStop, start, start.plusSeconds(1800), RunLabel("", ""))

    @Test fun `a route's runs keep the direction the server gives them`() {
        val trip = TripSummaryDto("t1", "Downtown", 1, "2026-10-08T07:00:00-07:00", "2026-10-08T07:30:00-07:00", "A", "B")
        val page = RouteTripsDto("R7", "20261008", "America/Los_Angeles", listOf(trip, trip.copy(id = "t2", directionId = null))).toRunPage()
        assertEquals(listOf(1, null), page.runs.map { it.directionId })
    }

    @Test fun `two directions are labelled by the headsign most of their runs show`() {
        val runs = listOf(
            run("a", 1, "Mount Baker", "Pine St", "Mount Baker TC"),
            run("b", 0, "Downtown Seattle Via Rainier Ave S"),
            run("c", 0, "Downtown Seattle", "Mount Baker TC", "Pine St"),
            run("d", 0, "Downtown Seattle", "Mount Baker TC", "Pine St"),
        )
        val directions = directionsOf(runs, highlightedId = "a")
        assertEquals(
            listOf(
                RunDirection(0, "Downtown Seattle", "Mount Baker TC", "Pine St"),
                RunDirection(1, "Mount Baker", "Pine St", "Mount Baker TC"),
            ),
            directions?.directions,
        )
        // The run under way is outbound, so the board opens there.
        assertEquals(1, directions?.start)
    }

    @Test fun `a tie goes to the headsign that comes first`() {
        val runs = listOf(run("a", 0, "Downtown"), run("b", 0, "Pine St"), run("c", 1, "Mount Baker"))
        assertEquals("Downtown", directionsOf(runs, null)?.directions?.first()?.headsign)
    }

    @Test fun `once the day's runs are over the board opens on the first direction`() {
        val runs = listOf(run("a", 1, "Mount Baker"), run("b", 0, "Downtown"))
        assertEquals(0, directionsOf(runs, highlightedId = null)?.start)
    }

    @Test fun `runs that do not split into two directions keep one list`() {
        assertNull("one direction", directionsOf(listOf(run("a", 0, "Downtown"), run("b", 0, "Downtown")), null))
        assertNull("no direction ids", directionsOf(listOf(run("a", null, "Downtown"), run("b", null, "Mount Baker")), null))
        assertNull("only some ids", directionsOf(listOf(run("a", 0, "Downtown"), run("b", 1, "Mount Baker"), run("c", null, "Loop")), null))
        assertNull("three directions", directionsOf(listOf(run("a", 0, "Downtown"), run("b", 1, "Mount Baker"), run("c", 2, "Loop")), null))
        assertNull("no runs", directionsOf(emptyList(), null))
    }
}
