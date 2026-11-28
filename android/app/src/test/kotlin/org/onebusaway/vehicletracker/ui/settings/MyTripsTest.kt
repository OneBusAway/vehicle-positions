package org.onebusaway.vehicletracker.ui.settings

import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.MyTrip
import org.onebusaway.vehicletracker.data.MyTripRoute
import org.onebusaway.vehicletracker.data.MyTripsPage
import java.time.Duration
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.Locale

/** My trips as it pages and refreshes, its days, and its rows: the cases iOS's MyTripsTests has, and the roll-over. */
class MyTripsTest {
    private val zone = ZoneId.of("America/Los_Angeles")

    /** 2026-11-25 13:00 in Los Angeles, a Wednesday. */
    private val now = ZonedDateTime.of(2026, 11, 25, 13, 0, 0, 0, zone)

    private fun at(year: Int, month: Int, day: Int, hour: Int, minute: Int) = ZonedDateTime.of(year, month, day, hour, minute, 0, 0, zone).toInstant()

    private val route7 = MyTripRoute("7", "Rainier Beach", "FFC72C", "000000")

    private fun trip(
        id: Long,
        start: java.time.Instant = now.toInstant(),
        ended: Boolean = true,
        headsign: String = "Downtown Seattle",
        route: MyTripRoute? = route7,
        routeId: String = "100479",
    ) = MyTrip(id, "4317", routeId, route, headsign, start, if (ended) start.plus(Duration.ofMinutes(49)) else null)

    private fun page(ids: LongRange, hasMore: Boolean, running: Long? = null) =
        MyTripsPage(hasMore, ids.reversed().map { trip(it, ended = it != running) })

    // --- The list ---

    @Test fun `the first page leaves the running trip out, but counts it for the next`() {
        var list = MyTripsList()
        assertEquals(MyTripsLoad.LOADING, list.load)
        assertNull(list.nextPage)

        list = list.receivedFirst(page(51L..100L, hasMore = true, running = 100L))

        assertEquals(MyTripsLoad.LOADED, list.load)
        assertEquals((99L downTo 51L).toList(), list.trips.map { it.id })
        assertEquals(50, list.nextPage?.offset)
    }

    @Test fun `the next page goes on the end, a trip already here shows once, and the offset counts what the server sent`() {
        var list = MyTripsList().receivedFirst(page(51L..100L, hasMore = true))
        val next = list.nextPage!!

        // A trip started since pushed trip 51 onto the second page.
        list = list.received(page(2L..51L, hasMore = true), next)

        assertEquals((100L downTo 2L).toList(), list.trips.map { it.id })
        val third = list.nextPage!!
        assertEquals("where the server's rows go on, the repeat counted", 100, third.offset)

        list = list.received(page(1L..1L, hasMore = false), third)
        assertEquals((100L downTo 1L).toList(), list.trips.map { it.id })
        assertNull("no more to load", list.nextPage)
    }

    @Test fun `a page asked for before a refresh is dropped`() {
        var list = MyTripsList().receivedFirst(page(51L..100L, hasMore = true))
        val before = list.nextPage!!

        list = list.receivedFirst(page(52L..101L, hasMore = true))
        list = list.received(page(1L..50L, hasMore = false), before)
        list = list.failed(before)

        assertEquals((101L downTo 52L).toList(), list.trips.map { it.id })
        assertFalse(list.moreFailed)
        assertNotEquals("the same offset, asked for again after the refresh", before, list.nextPage)
        assertEquals(50, list.nextPage?.offset)
    }

    @Test fun `a failed next page waits for Try again`() {
        var list = MyTripsList().receivedFirst(page(51L..100L, hasMore = true))
        val next = list.nextPage!!

        list = list.failed(next)
        assertTrue(list.moreFailed)
        assertNull("the end of the list stops loading until Try again", list.nextPage)

        list = list.retriedMore()
        assertEquals(next, list.nextPage)
    }

    @Test fun `a failed refresh keeps the list, and a failed first load takes the screen`() {
        val first = MyTripsList().firstFailed()
        assertEquals(MyTripsLoad.FAILED, first.load)
        assertEquals(MyTripsLoad.LOADING, first.reloaded().load)

        var list = MyTripsList().receivedFirst(page(1L..3L, hasMore = false)).firstFailed()
        assertEquals(MyTripsLoad.LOADED, list.load)
        assertTrue(list.refreshFailed)
        assertEquals(3, list.trips.size)

        list = list.receivedFirst(page(1L..4L, hasMore = false))
        assertFalse(list.refreshFailed)
        assertEquals(4, list.trips.size)
    }

    // --- Days ---

    @Test fun `trips go under the days they started on, in the phone's time zone`() {
        val trips = listOf(
            trip(6, at(2026, 11, 25, 7, 13)),
            trip(5, at(2026, 11, 25, 6, 5)),
            // 23:30 in Los Angeles is already the 25th in UTC.
            trip(4, at(2026, 11, 24, 23, 30)),
            trip(3, at(2026, 11, 23, 9, 20)),
            trip(2, at(2025, 12, 31, 8, 10)),
        )

        val days = myTripsDays(trips, zone)

        assertEquals(
            listOf(LocalDate.of(2026, 11, 25), LocalDate.of(2026, 11, 24), LocalDate.of(2026, 11, 23), LocalDate.of(2025, 12, 31)),
            days.map { it.date },
        )
        assertEquals(listOf(listOf(6L, 5L), listOf(4L), listOf(3L), listOf(2L)), days.map { day -> day.trips.map { it.id } })
    }

    @Test fun `Today, Yesterday, then the date, with the year only for another year`() {
        val today = LocalDate.of(2026, 11, 25)
        fun title(date: LocalDate) = dayTitle(date, today, "Today", "Yesterday") { day, withYear -> "$day${if (withYear) " +year" else ""}" }

        assertEquals("Today", title(today))
        assertEquals("Yesterday", title(LocalDate.of(2026, 11, 24)))
        assertEquals("2026-11-23", title(LocalDate.of(2026, 11, 23)))
        assertEquals("2025-12-31 +year", title(LocalDate.of(2025, 12, 31)))
    }

    // Aaron on #687: a page left open past midnight says Today and Yesterday of the new day.
    @Test fun `today moves on at midnight`() = runTest {
        val start = ZonedDateTime.of(2026, 11, 25, 23, 58, 0, 0, zone)
        val seen = mutableListOf<LocalDate>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            todays { start.plus(Duration.ofMillis(currentTime)) }.toList(seen)
        }

        assertEquals(listOf(LocalDate.of(2026, 11, 25)), seen)
        advanceTimeBy(Duration.ofMinutes(2).toMillis())
        runCurrent()
        assertEquals(listOf(LocalDate.of(2026, 11, 25), LocalDate.of(2026, 11, 26)), seen)
        advanceTimeBy(Duration.ofHours(24).toMillis())
        runCurrent()
        assertEquals(LocalDate.of(2026, 11, 27), seen.last())
        assertEquals("once a day", 3, seen.size)
    }

    // --- Rows ---

    @Test fun `a row has the route's badge, where the trip went, and when and on which vehicle`() {
        val row = myTripRow(trip(1, at(2026, 11, 25, 7, 13)), zone, Locale.US)

        assertEquals("7", row.badge)
        assertEquals("FFC72C", row.color)
        assertEquals("000000", row.textColor)
        assertEquals("Downtown Seattle", row.title)
        // The JDK's CLDR data puts a narrow no-break space before AM.
        assertEquals("7:13 AM", row.start.replace(' ', ' '))
        assertEquals("8:02 AM", row.end.replace(' ', ' '))
        assertEquals("4317", row.vehicle)
    }

    @Test fun `with no headsign the route's name shows, and a route the schedule lost shows its id`() {
        assertEquals("Rainier Beach", myTripRow(trip(1, headsign = ""), zone, Locale.US).title)

        val lost = myTripRow(trip(2, headsign = "", route = null), zone, Locale.US)
        assertEquals("100479", lost.badge)
        assertEquals("the theme's colours, as any badge with none", "", lost.color)
        assertNull(lost.title)

        assertEquals("100479", myTripRow(trip(3, route = MyTripRoute("", "Loop", "", "")), zone, Locale.US).badge)
        assertEquals("", myTripRow(trip(4, headsign = "", route = null, routeId = ""), zone, Locale.US).badge)
    }
}
