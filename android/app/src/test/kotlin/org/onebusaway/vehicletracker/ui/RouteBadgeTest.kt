package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.routes.routeColorArgb
import org.onebusaway.vehicletracker.ui.routes.routeColorHex

class RouteBadgeTest {
    @Test fun `a route colour is read as opaque ARGB`() {
        assertEquals(0xFF0077C0L, routeColorArgb("0077C0"))
        // GTFS omits the '#', but a feed that includes one still means the same colour.
        assertEquals(0xFF0077C0L, routeColorArgb("#0077C0"))
        assertEquals(0xFF000000L, routeColorArgb("000000"))
    }

    @Test fun `a missing or unreadable colour leaves the badge to the theme`() {
        // route_color is optional in GTFS, and the server passes "" through as "".
        assertNull(routeColorArgb(""))
        assertNull(routeColorArgb("0077C"))
        assertNull(routeColorArgb("ZZZZZZ"))
    }

    @Test fun `a colour goes back to the feed's six hex digits for the next screen`() {
        assertEquals("9C182F", routeColorHex(Color(0xFF9C182F)))
        assertEquals("00A0E0", routeColorHex(Color(0xFF00A0E0)))
        assertEquals("", routeColorHex(null))
    }

    @Test fun `a badge model reads both of the feed's colours once`() {
        val model = RouteBadgeModel.of("E Line", "9C182F", "FFFFFF")
        assertEquals(RouteBadgeModel("E Line", Color(0xFF9C182F), Color(0xFFFFFFFF)), model)
        assertEquals(RouteBadgeModel("7", null, null), RouteBadgeModel.of("7", "", "nope"))
    }

    @Test fun `an open trip's badge comes from its stored geometry, or its route id without one`() {
        val trip = ActiveTrip(7L, "T1", "bus-1", "100263", "20260902", 100L)
        val geometry = TripFixtures.t1.copy(routeShortName = "7", routeColor = "FDB71A", routeTextColor = "000000")

        assertEquals(RouteBadgeModel.of("7", "FDB71A", "000000"), RouteBadgeModel.forTrip(trip, geometry))
        assertEquals(RouteBadgeModel("100263", null, null), RouteBadgeModel.forTrip(trip, null))
        // A trip stored before the route's name was kept still has its colours.
        assertEquals(RouteBadgeModel.of("100263", "FDB71A", ""), RouteBadgeModel.forTrip(trip, TripFixtures.t1.copy(routeColor = "FDB71A")))
    }
}
