package org.onebusaway.vehicletracker.ui.settings

import android.os.Build
import androidx.compose.ui.unit.dp
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.groupPlace
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.settings.StopState.ALLOWED
import org.onebusaway.vehicletracker.ui.settings.StopState.BEYOND
import org.onebusaway.vehicletracker.ui.settings.StopState.MISSING

class ReadinessTest {
    private val everything = Grants(
        sdk = Build.VERSION_CODES.BAKLAVA,
        fine = true,
        coarse = true,
        background = true,
        notifications = true,
        batteryExempt = true,
    )
    private val nothing = everything.copy(fine = false, coarse = false, background = false, notifications = false, batteryExempt = false)

    private fun of(grants: Grants = everything, locationServicesOn: Boolean = true, waiting: ReportsWaiting? = null) =
        readiness(PhoneAccess(grants, locationServicesOn), waiting)

    /** Past the last of the six checks. */
    private val byTheRiders = 7

    @Test fun `with everything allowed the vehicle is by the riders`() {
        val ready = of()

        assertEquals(ReadinessState.Ready(emptyList(), null), ready.state)
        assertTrue(ready.canReport)
        assertEquals(List(6) { ALLOWED }, ready.stops)
        assertEquals(byTheRiders, ready.vehicleAt)
    }

    @Test fun `there is a stop for every check of the checklist`() {
        assertEquals(PermissionRow.entries.size, of().stops.size)
    }

    @Test fun `each check a trip can do without is one thing to fix, and the vehicle gets past it`() {
        val cases = mapOf(
            PermissionRow.BACKGROUND to everything.copy(background = false),
            PermissionRow.NOTIFICATIONS to everything.copy(notifications = false),
            PermissionRow.BATTERY to everything.copy(batteryExempt = false),
        )
        for ((row, grants) in cases) {
            val ready = of(grants)

            assertEquals("$row", ReadinessState.Ready(listOf(row), null), ready.state)
            assertEquals("$row", byTheRiders, ready.vehicleAt)
            assertEquals("$row", PermissionRow.entries.map { if (it == row) MISSING else ALLOWED }, ready.stops)
        }
    }

    @Test fun `approximate location can report, and precise location is to fix`() {
        val ready = of(everything.copy(fine = false))

        assertTrue(ready.canReport)
        assertTrue(PermissionRow.PRECISE in (ready.state as ReadinessState.Ready).toFix)
        assertEquals(MISSING, ready.stops[PermissionRow.PRECISE.ordinal])
    }

    @Test fun `the things to fix are in the checklist's order`() {
        val state = of(everything.copy(batteryExempt = false, notifications = false, background = false)).state

        assertEquals(
            listOf(PermissionRow.BACKGROUND, PermissionRow.NOTIFICATIONS, PermissionRow.BATTERY),
            (state as ReadinessState.Ready).toFix,
        )
    }

    @Test fun `no location stops the vehicle before the first stop, and what Android gives only with it waits`() {
        val refused = of(nothing)

        assertEquals(ReadinessState.LocationRefused, refused.state)
        assertFalse(refused.canReport)
        assertEquals(0, refused.vehicleAt)
        assertEquals(listOf(MISSING, BEYOND, BEYOND, MISSING, MISSING, ALLOWED), refused.stops)
    }

    @Test fun `a check allowed past the break still shows allowed`() {
        val refused = of(nothing.copy(notifications = true, batteryExempt = true))

        assertEquals(listOf(MISSING, BEYOND, BEYOND, ALLOWED, ALLOWED, ALLOWED), refused.stops)
    }

    @Test fun `location off for the phone stops the vehicle on the stop before it`() {
        val off = of(locationServicesOn = false)

        assertEquals(ReadinessState.LocationOff, off.state)
        assertFalse(off.canReport)
        assertEquals("on battery's stop, the fifth", 5, off.vehicleAt)
        assertEquals(listOf(ALLOWED, ALLOWED, ALLOWED, ALLOWED, ALLOWED, MISSING), off.stops)
    }

    @Test fun `a refusal is said before the phone's switch, which would not help alone`() {
        assertEquals(ReadinessState.LocationRefused, of(nothing, locationServicesOn = false).state)
    }

    @Test fun `with location off a check a trip can do without is still missing on its stop`() {
        val off = of(everything.copy(batteryExempt = false), locationServicesOn = false)

        assertEquals(ReadinessState.LocationOff, off.state)
        assertEquals(MISSING, off.stops[PermissionRow.BATTERY.ordinal])
    }

    @Test fun `reports waiting ride along with ready, and never with what stops a trip`() {
        val waiting = ReportsWaiting(12, WaitingOutlook.WILL_SEND)

        assertEquals(ReadinessState.Ready(emptyList(), waiting), of(waiting = waiting).state)
        assertEquals(ReadinessState.LocationRefused, of(nothing, waiting = waiting).state)
    }

    @Test fun `what Android below 10 and 13 does not ask for is allowed, so there are always six stops`() {
        val old = Grants(sdk = Build.VERSION_CODES.P, fine = true, coarse = true, background = false, notifications = false, batteryExempt = true)

        assertEquals(List(6) { ALLOWED }, of(old).stops)
    }

    // --- Along the line ---

    @Test fun `the vehicle rests clear of the rider, and the stops are evenly between`() {
        for (width in listOf(320.dp, 360.dp, 412.dp, 800.dp)) {
            val line = LineGeometry(width, stops = 6)
            val places = (0..7).map { line.at(it.toFloat()).value }
            val gaps = places.zipWithNext { a, b -> b - a }

            assertEquals("$width", line.riderAt.value - 42f, places.last(), 0.01f)
            assertTrue("$width: all the same way", gaps.all { it > 0f })
            assertEquals("$width: evenly", gaps.max(), gaps.min(), 0.01f)
        }
    }

    @Test fun `the icons over the stops need room, which the narrowest phone has not`() {
        assertFalse(LineGeometry(320.dp, stops = 6).hasRoomForIcons)
        assertTrue(LineGeometry(360.dp, stops = 6).hasRoomForIcons)
        assertTrue(LineGeometry(412.dp, stops = 6).hasRoomForIcons)
    }

    @Test fun `a part of the fold runs from 0 to 1 between its ends`() {
        assertEquals(0f, within(0.1f, 0.3f, 0.7f), 0f)
        assertEquals(0.5f, within(0.5f, 0.3f, 0.7f), 0.001f)
        assertEquals(1f, within(0.9f, 0.3f, 0.7f), 0f)
    }

    // --- The rows and the account ---

    @Test fun `a group's only row is round all round, and a group of more is joined`() {
        assertEquals(listOf(GroupPlace.ONLY), List(1) { groupPlace(it, 1) })
        assertEquals(listOf(GroupPlace.FIRST, GroupPlace.LAST), List(2) { groupPlace(it, 2) })
        assertEquals(listOf(GroupPlace.FIRST, GroupPlace.MIDDLE, GroupPlace.LAST), List(3) { groupPlace(it, 3) })
    }

    @Test fun `an address may break after its at sign and before each dot, and nowhere else`() {
        assertEquals("maria@|metro|.example", breakable("maria@metro.example").replace('\u200B', '|'))
        assertEquals("tracker|.example|.org:8443", breakable("tracker.example.org:8443").replace('\u200B', '|'))
        assertEquals("localhost", breakable("localhost"))
    }
}
