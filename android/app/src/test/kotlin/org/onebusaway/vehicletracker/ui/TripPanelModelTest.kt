package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.map.MapFailure
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.tracking.Reporting
import org.onebusaway.vehicletracker.ui.tracking.StopKind
import org.onebusaway.vehicletracker.ui.tracking.StopRow
import org.onebusaway.vehicletracker.ui.tracking.StreetMap
import org.onebusaway.vehicletracker.ui.tracking.panelRowsAlpha
import org.onebusaway.vehicletracker.ui.tracking.reporting
import org.onebusaway.vehicletracker.ui.tracking.stopRows
import org.onebusaway.vehicletracker.ui.tracking.streetMap
import java.io.File

class TripPanelModelTest {
    private val stops = TripFixtures.t1.stops
    private val st1 = stops[0]
    private val st2 = stops[1]
    private val st3 = stops[2]

    @Test fun `the vehicle sits between the stop it passed and the next, with the rest after`() {
        assertEquals(
            listOf(StopRow.Stop(st1, StopKind.PASSED), StopRow.Vehicle, StopRow.Stop(st2, StopKind.NEXT), StopRow.Stop(st3, StopKind.LAST)),
            stopRows(stops, nextStopIndex = 1, unfolded = false),
        )
    }

    @Test fun `before the first stop there is no stop behind the vehicle`() {
        assertEquals(
            listOf(StopRow.Vehicle, StopRow.Stop(st1, StopKind.NEXT), StopRow.Stop(st2, StopKind.UPCOMING), StopRow.Stop(st3, StopKind.LAST)),
            stopRows(stops, nextStopIndex = 0, unfolded = false),
        )
    }

    @Test fun `heading for the last stop, it is the next stop and nothing follows`() {
        assertEquals(
            listOf(StopRow.Folded(1), StopRow.Stop(st2, StopKind.PASSED), StopRow.Vehicle, StopRow.Stop(st3, StopKind.NEXT)),
            stopRows(stops, nextStopIndex = 2, unfolded = false),
        )
        // Open, the row stays at the top to fold them again, with the stop it stands for under it.
        assertEquals(
            listOf(
                StopRow.Folded(1),
                StopRow.Stop(st1, StopKind.PASSED),
                StopRow.Stop(st2, StopKind.PASSED),
                StopRow.Vehicle,
                StopRow.Stop(st3, StopKind.NEXT),
            ),
            stopRows(stops, nextStopIndex = 2, unfolded = true),
        )
    }

    @Test fun `the passed stops above the last one fold into one row, and open under it in order`() {
        val long = (1..8).map { n ->
            TripStop("S$n", "Stop $n", 47.6, -122.33, alongShapeM = n * 100.0, arrivalAt = at(8, n), departureAt = at(8, n))
        }

        assertEquals(
            listOf(
                StopRow.Folded(5),
                StopRow.Stop(long[5], StopKind.PASSED),
                StopRow.Vehicle,
                StopRow.Stop(long[6], StopKind.NEXT),
                StopRow.Stop(long[7], StopKind.LAST),
            ),
            stopRows(long, nextStopIndex = 6, unfolded = false),
        )
        assertEquals(
            listOf(StopRow.Folded(5)) + long.take(6).map { StopRow.Stop(it, StopKind.PASSED) } +
                listOf(StopRow.Vehicle, StopRow.Stop(long[6], StopKind.NEXT), StopRow.Stop(long[7], StopKind.LAST)),
            stopRows(long, nextStopIndex = 6, unfolded = true),
        )
        // Even one stop above the last passed folds, so the list always opens the same way.
        assertEquals(
            listOf(StopRow.Folded(1), StopRow.Stop(long[1], StopKind.PASSED), StopRow.Vehicle),
            stopRows(long, nextStopIndex = 2, unfolded = false).take(3),
        )
        // The last passed stop alone does not fold, and before any stop there is nothing to fold.
        assertEquals(StopRow.Stop(long[0], StopKind.PASSED), stopRows(long, nextStopIndex = 1, unfolded = false).first())
        assertEquals(StopRow.Vehicle, stopRows(long, nextStopIndex = 0, unfolded = false).first())
    }

    @Test fun `with no fix the whole run shows, from its first stop, and no vehicle`() {
        val whole = listOf(StopRow.Stop(st1, StopKind.NEXT), StopRow.Stop(st2, StopKind.UPCOMING), StopRow.Stop(st3, StopKind.LAST))

        assertEquals(whole, stopRows(stops, nextStopIndex = null, unfolded = false))
        // A stop the trip does not have is no fix to place the vehicle by.
        assertEquals(whole, stopRows(stops, nextStopIndex = -1, unfolded = false))
        assertEquals(emptyList<StopRow>(), stopRows(emptyList(), nextStopIndex = null, unfolded = false))
    }

    @Test fun `off the route the vehicle still sits before the stop it is heading for`() {
        val offRoute = requireNotNull(AdherenceEvaluator.of(TripFixtures.t1)).evaluate(fix(47.6045, -122.3200, at(8, 5)), previous = null)
        require(offRoute.status == AdherenceStatus.OFF_ROUTE)

        val rows = stopRows(stops, stops.indexOf(offRoute.nextStop), unfolded = false)

        assertEquals(StopRow.Stop(offRoute.nextStop, StopKind.NEXT), rows[rows.indexOf(StopRow.Vehicle) + 1])
    }

    @Test fun `the rows under the bar stay hidden while the panel is down and come in as it is pulled up`() {
        assertEquals(0f, panelRowsAlpha(collapsedTop = 1000f, sheetTop = 1000f, fadePx = 100f), 0f)
        assertEquals(0.5f, panelRowsAlpha(collapsedTop = 1000f, sheetTop = 950f, fadePx = 100f), 1e-6f)
        assertEquals(1f, panelRowsAlpha(collapsedTop = 1000f, sheetTop = 400f, fadePx = 100f), 0f)
        // Before the panel is placed, and when it settles a pixel low.
        assertEquals(0f, panelRowsAlpha(collapsedTop = 1000f, sheetTop = null, fadePx = 100f), 0f)
        assertEquals(0f, panelRowsAlpha(collapsedTop = 1000f, sheetTop = 1001f, fadePx = 100f), 0f)
        // No fade distance is no division by zero: a panel that is down stays hidden, not NaN.
        assertEquals(0f, panelRowsAlpha(collapsedTop = 1000f, sheetTop = 1000f, fadePx = 0f), 0f)
    }

    @Test fun `reporting says what each problem means for the reports`() {
        assertEquals(Reporting.REPORTING, reporting(TrackingProblem.NONE))
        assertEquals(Reporting.NOT_REPORTING, reporting(TrackingProblem.NO_NETWORK))
        // The server refuses a wrong clock's reports, so they are not reaching riders either.
        assertEquals(Reporting.NOT_REPORTING, reporting(TrackingProblem.CLOCK_SKEW))
        assertEquals(Reporting.NO_GPS, reporting(TrackingProblem.NO_GPS))
        assertEquals(Reporting.STOPPED, reporting(TrackingProblem.AUTH_EXPIRED))
        // Refused reports reach no rider either, until dispatch fixes the vehicle or the trip.
        assertEquals(Reporting.NOT_REPORTING, reporting(TrackingProblem.REPORTS_REFUSED))
        assertFalse(Reporting.REPORTING.isProblem)
        assertTrue(Reporting.entries.filter { it != Reporting.REPORTING }.all { it.isProblem })
    }

    @Test fun `the street map row says where the file stands, and is hidden with no map`() {
        assertNull(streetMap(MapFileState.None))
        assertEquals(StreetMap.Ready, streetMap(MapFileState.Ready(File("map.pmtiles"))))
        assertEquals(StreetMap.Downloading(42), streetMap(MapFileState.Downloading(bytes = 42, total = 100)))
        assertEquals(StreetMap.NotDownloaded, streetMap(MapFileState.Failed(MapFailure.NETWORK)))
        assertEquals(StreetMap.NotDownloaded, streetMap(MapFileState.Failed(MapFailure.UNEXPECTED)))
        assertEquals(StreetMap.NoSpace, streetMap(MapFileState.Failed(MapFailure.NO_SPACE)))
        assertEquals(StreetMap.Unusable, streetMap(MapFileState.Failed(MapFailure.INVALID_FILE)))
        assertEquals(StreetMap.Unusable, streetMap(MapFileState.Failed(MapFailure.CLEARTEXT_BLOCKED)))
    }
}
