package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.RouteTripsDto
import org.onebusaway.vehicletracker.data.api.TripSummaryDto
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.TripRun
import org.onebusaway.vehicletracker.ui.runs.clockTime
import org.onebusaway.vehicletracker.ui.runs.highlightOf
import org.onebusaway.vehicletracker.ui.runs.highlightedRun
import org.onebusaway.vehicletracker.ui.runs.serviceDateLabel
import org.onebusaway.vehicletracker.ui.runs.toRunPage
import java.time.Instant
import java.time.LocalTime
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.ZoneOffset

/**
 * The Now/Next rule and the time parsing behind it, ported from
 * `ios/VehicleTracker/UI/Formatters.swift` and its `TripRunTests`.
 */
class HighlightedRunTest {
    private fun at(hour: Int, minute: Int): Instant =
        OffsetDateTime.parse("2026-09-22T00:00:00Z").plusHours(hour.toLong()).plusMinutes(minute.toLong()).toInstant()

    private fun run(id: String, start: Instant, end: Instant) =
        TripRun(id = id, headsign = "North", directionId = 0, firstStop = "A", lastStop = "B", startsAt = start, endsAt = end)

    /**
     * A frequent route, like the 1 Line in #185: a run every 10 minutes from 13:00 to 15:00, each an hour long, so six
     * are under way at once. Each run's id is its start time, "13:00" to "15:00". The same table as iOS's
     * `TripFixtures.frequentService`.
     */
    private val frequentService = (0..120 step 10).map { minutes ->
        val start = at(13 + minutes / 60, minutes % 60)
        run("${13 + minutes / 60}:${(minutes % 60).toString().padStart(2, '0')}", start, start.plusSeconds(3600))
    }

    private fun trip(id: String, startsAt: String, endsAt: String) = TripSummaryDto(
        id = id,
        headsign = "North",
        directionId = null,
        startsAt = startsAt,
        endsAt = endsAt,
        firstStop = "A",
        lastStop = "B",
    )

    @Test fun `picks the run containing now`() {
        val runs = listOf(
            run("a", at(7, 0), at(7, 30)),
            run("b", at(8, 0), at(8, 30)),
            run("c", at(9, 0), at(9, 30)),
        )

        assertEquals("b", highlightedRun(runs, at(8, 10))?.id)
    }

    @Test fun `otherwise picks the next to start`() {
        val runs = listOf(run("a", at(7, 0), at(7, 30)), run("c", at(9, 0), at(9, 30)))

        assertEquals("c", highlightedRun(runs, at(8, 10))?.id)
    }

    @Test fun `no run once every one of them is over`() {
        assertNull(highlightedRun(listOf(run("a", at(7, 0), at(7, 30))), at(8, 10)))
        assertNull(highlightedRun(emptyList(), at(8, 10)))
    }

    @Test fun `a tie goes to the later start`() {
        val runs = listOf(run("a", at(8, 0), at(8, 30)), run("b", at(9, 0), at(9, 30)))

        assertEquals("a", highlightedRun(runs, at(8, 29))?.id)
        // a started 30 minutes ago and b starts in 30. a is already under way, so the driver is about to drive b.
        assertEquals("b", highlightedRun(runs, at(8, 30))?.id)
    }

    @Test fun `a run ending exactly now still counts`() {
        val runs = listOf(run("a", at(8, 0), at(8, 30)))

        assertEquals("a", highlightedRun(runs, at(8, 30))?.id)
        // One second past the end and the run is over.
        assertNull(highlightedRun(runs, at(8, 30).plusSeconds(1)))
    }

    @Test fun `on a frequent route, picks the run starting nearest now`() {
        // Six runs are under way at 14:03; the earliest of them, 13:10, is nearly over. The driver is about to drive the 14:00.
        val now = highlightedRun(frequentService, at(14, 3))
        assertEquals("14:00", now?.id)
        assertEquals(RunHighlight.NOW, now?.let { highlightOf(it, at(14, 3)) })

        val next = highlightedRun(frequentService, at(14, 7))
        assertEquals("14:10", next?.id)
        assertEquals(RunHighlight.NEXT, next?.let { highlightOf(it, at(14, 7)) })
    }

    @Test fun `skips a run that has ended, even with the nearest start`() {
        val runs = listOf(run("a", at(8, 0), at(8, 5)), run("b", at(8, 20), at(8, 40)))

        assertEquals("b", highlightedRun(runs, at(8, 7))?.id)
    }

    @Test fun `on a sparse route, keeps the run under way`() {
        val runs = listOf(run("a", at(6, 0), at(10, 0)), run("b", at(14, 0), at(15, 0)))

        assertEquals("a", highlightedRun(runs, at(9, 0))?.id)
    }

    @Test fun `the badge says Now once a run has started and Next before that`() {
        val run = run("a", at(8, 0), at(8, 30))

        assertEquals(RunHighlight.NEXT, highlightOf(run, at(7, 59)))
        assertEquals(RunHighlight.NOW, highlightOf(run, at(8, 0)))
        assertEquals(RunHighlight.NOW, highlightOf(run, at(8, 30)))
    }

    @Test fun `run times parse an offset, a Z, and fractional seconds`() {
        val page = RouteTripsDto(
            routeId = "R1",
            serviceDate = "20260922",
            timezone = "Africa/Nairobi",
            trips = listOf(
                trip("offset", "2026-09-22T07:02:00+03:00", "2026-09-22T07:40:00+03:00"),
                trip("zulu", "2026-09-22T04:02:00Z", "2026-09-22T04:40:00Z"),
                trip("fractional", "2026-09-22T04:02:00.5Z", "2026-09-22T04:40:00.25+00:00"),
            ),
        ).toRunPage()

        assertEquals(ZoneId.of("Africa/Nairobi"), page.timezone)
        // Go emits an offset unless the agency's zone is UTC, and Instant.parse rejects one —
        // this is the case that would throw if these were read as instants.
        assertEquals(Instant.parse("2026-09-22T04:02:00Z"), page.runs[0].startsAt)
        assertEquals(page.runs[0].startsAt, page.runs[1].startsAt)
        assertEquals(Instant.parse("2026-09-22T04:02:00.5Z"), page.runs[2].startsAt)
        assertEquals(Instant.parse("2026-09-22T04:40:00.25Z"), page.runs[2].endsAt)
    }

    @Test fun `clock times are read in the response's timezone, not the device's`() {
        val startsAt = OffsetDateTime.parse("2026-09-22T07:02:00+03:00").toInstant()

        assertEquals(LocalTime.of(7, 2), clockTime(startsAt, ZoneId.of("Africa/Nairobi")))
        // The same instant on a phone left in UTC: the list still has to read the agency's 07:02.
        assertEquals(LocalTime.of(4, 2), clockTime(startsAt, ZoneOffset.UTC))
    }

    @Test fun `the service date reads as a date`() {
        assertEquals("2026-09-22", serviceDateLabel("20260922"))
        // Anything the server sends that is not a service date is shown as it came.
        assertEquals("", serviceDateLabel(""))
    }
}
