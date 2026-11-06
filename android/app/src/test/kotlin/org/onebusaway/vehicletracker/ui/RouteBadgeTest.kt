package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.map.contrast
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.routes.legibleTextOn
import org.onebusaway.vehicletracker.ui.routes.routeColorArgb
import org.onebusaway.vehicletracker.ui.routes.routeColorHex

class RouteBadgeTest {
    private val themeText = Color(0xFF0A390F)

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

    @Test fun `a badge with no route colour takes the theme's text, whatever the feed says`() {
        assertEquals(themeText, RouteBadgeModel.of("7", "", "").foreground(themeText))
        // The feed chose this text colour for a route colour the badge doesn't have.
        assertEquals(themeText, RouteBadgeModel.of("7", "", "FFFFFF").foreground(themeText))
    }

    @Test fun `a route colour keeps the feed's own text colour`() {
        assertEquals(Color(0xFFFFFFFF), RouteBadgeModel.of("E Line", "9C182F", "FFFFFF").foreground(themeText))
        // No contrast override: a route looks the way the agency's feed says it does.
        assertEquals(Color(0xFFFFFFFF), RouteBadgeModel.of("7", "FDB71A", "FFFFFF").foreground(themeText))
    }

    @Test fun `a route colour with no text colour gets black or white, whichever reads on it`() {
        assertEquals(Color.White, RouteBadgeModel.of("E Line", "9C182F", "").foreground(themeText))
        assertEquals(Color.Black, RouteBadgeModel.of("7", "FDB71A", "").foreground(themeText))
    }

    @Test fun `the text picked for a route colour always reaches 4,5 to 1`() {
        val steps = (0..255 step 17).map { it / 255f }
        for (red in steps) for (green in steps) for (blue in steps) {
            val background = Color(red, green, blue)
            val text = legibleTextOn(background)
            val ratio = contrast(text.toArgb(), background.toArgb())
            assertTrue("$text on $background is only $ratio:1", ratio >= 4.5)
        }
    }
}
