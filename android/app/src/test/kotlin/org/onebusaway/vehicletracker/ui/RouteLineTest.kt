package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.map.FALLBACK_ROUTE_COLOR
import org.onebusaway.vehicletracker.ui.map.PLAIN_BACKGROUND_DARK
import org.onebusaway.vehicletracker.ui.map.casingColor
import org.onebusaway.vehicletracker.ui.map.contrast
import org.onebusaway.vehicletracker.ui.map.liftedForNight
import org.onebusaway.vehicletracker.ui.map.routeLineColor
import org.onebusaway.vehicletracker.ui.map.routeLineString

class RouteLineTest {
    @Test fun `a GeoJSON position is longitude first`() {
        val line = requireNotNull(routeLineString(listOf(GeoPoint(47.6, -122.33), GeoPoint(47.6045, -122.33))))

        assertEquals("LineString", line.type())
        assertEquals(-122.33, line.coordinates()[0].longitude(), 0.0)
        assertEquals(47.6, line.coordinates()[0].latitude(), 0.0)
    }

    @Test fun `every shape point is kept, in order`() {
        val points = TripFixtures.loop.shapePoints
        val line = requireNotNull(routeLineString(points))

        assertEquals(points, line.coordinates().map { GeoPoint(it.latitude(), it.longitude()) })
    }

    @Test fun `fewer than two points is no line`() {
        assertNull(routeLineString(emptyList()))
        assertNull(routeLineString(listOf(GeoPoint(47.6, -122.33))))
    }

    @Test fun `the line takes the route colour, or system blue without one`() {
        assertEquals(0xFF0077C0.toInt(), routeLineColor("0077C0", dark = false))
        // route_color is optional in GTFS, and the server passes "" through as "".
        assertEquals(FALLBACK_ROUTE_COLOR, routeLineColor("", dark = false))
        assertEquals(FALLBACK_ROUTE_COLOR, routeLineColor("ZZZZZZ", dark = false))
    }

    /** Greys either side of each cut-off: #A0A0A0 is luminance 0.352 and #9F9F9F 0.347, #767676 0.181 and #757575 0.178. */
    @Test fun `on the light map a bright route has an ink casing and a dark one white`() {
        assertEquals(INK, casingColor(0xFFFDB71A.toInt(), dark = false))
        assertEquals(INK, casingColor(0xFFA0A0A0.toInt(), dark = false))
        assertEquals(WHITE, casingColor(0xFF9F9F9F.toInt(), dark = false))
        assertEquals(WHITE, casingColor(FALLBACK_ROUTE_COLOR, dark = false))
        assertEquals(WHITE, casingColor(0xFF9C182F.toInt(), dark = false))
    }

    @Test fun `on the dark map a bright route has a near-black casing and a dark one white`() {
        assertEquals(NIGHT_INK, casingColor(0xFFFDB71A.toInt(), dark = true))
        assertEquals(NIGHT_INK, casingColor(0xFF767676.toInt(), dark = true))
        assertEquals(WHITE, casingColor(0xFF757575.toInt(), dark = true))
    }

    @Test fun `at night a route too dark for the map is lifted until it reads, in the same hue`() {
        for (route in listOf(0xFF9C182F.toInt(), 0xFF000000.toInt(), 0xFF1F1F1F.toInt(), 0xFF002060.toInt())) {
            // Lifted until it reads, and no further: it keeps as much of its own colour as it can.
            val ratio = contrast(liftedForNight(route), PLAIN_BACKGROUND_DARK)
            assertTrue("${route.toString(16)} at $ratio:1", ratio >= 3.0 && ratio < 3.3)
        }
        // Maroon stays red: more red than green or blue.
        val maroon = liftedForNight(0xFF9C182F.toInt())
        assertTrue(red(maroon) > green(maroon) && red(maroon) > blue(maroon))
        // Black has no hue to keep, and lifts to a grey.
        val black = liftedForNight(0xFF000000.toInt())
        assertTrue(red(black) == green(black) && green(black) == blue(black))
    }

    @Test fun `a route that already reads at night is left as it is`() {
        assertEquals(0xFFFDB71A.toInt(), liftedForNight(0xFFFDB71A.toInt()))
        assertEquals(FALLBACK_ROUTE_COLOR, routeLineColor("", dark = true))
        assertEquals(0xFF0077C0.toInt(), routeLineColor("0077C0", dark = true))
    }

    @Test fun `only the dark map lifts a route`() {
        assertEquals(0xFF9C182F.toInt(), routeLineColor("9C182F", dark = false))
        assertTrue(routeLineColor("9C182F", dark = true) != 0xFF9C182F.toInt())
    }

    private fun red(argb: Int) = (argb ushr 16) and 0xFF
    private fun green(argb: Int) = (argb ushr 8) and 0xFF
    private fun blue(argb: Int) = argb and 0xFF

    private companion object {
        const val INK = 0xFF1F1F1F.toInt()
        const val NIGHT_INK = 0xFF0A0A0A.toInt()
        const val WHITE = 0xFFFFFFFF.toInt()
    }
}
