package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.map.FALLBACK_ROUTE_COLOR
import org.onebusaway.vehicletracker.ui.map.casingColor
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
        assertEquals(0xFF0077C0.toInt(), routeLineColor("0077C0"))
        // route_color is optional in GTFS, and the server passes "" through as "".
        assertEquals(FALLBACK_ROUTE_COLOR, routeLineColor(""))
        assertEquals(FALLBACK_ROUTE_COLOR, routeLineColor("ZZZZZZ"))
    }

    @Test fun `the casing is the route colour 35 percent darker`() {
        assertEquals(0xFF004D7D.toInt(), casingColor(0xFF0077C0.toInt()))
        assertEquals(0xFF004FA6.toInt(), casingColor(FALLBACK_ROUTE_COLOR))
        assertEquals(0xFFA6A6A6.toInt(), casingColor(0xFFFFFFFF.toInt()))
        assertEquals(0xFF000000.toInt(), casingColor(0xFF000000.toInt()))
    }

    @Test fun `the casing keeps the route colour's alpha`() {
        assertEquals(0x80004D7D.toInt(), casingColor(0x800077C0.toInt()))
    }

    /**
     * What iOS draws: each expected value is AppKit's own `getHue` and `NSColor(hue:saturation:
     * brightness:)` with brightness times 0.65, rounded to 8 bits. Pins the claim in `casingColor`'s
     * doc that scaling the channels is the same colour as cutting HSB brightness.
     */
    @Test fun `the casing matches cutting HSB brightness as iOS does`() {
        val apple = mapOf(
            0xE51A8C to 0x95115B,
            0x7FBF3F to 0x537C29,
            0xFFD400 to 0xA68A00,
        )
        for ((route, casing) in apple) {
            assertEquals(route.toString(16), 0xFF000000.toInt() or casing, casingColor(0xFF000000.toInt() or route))
        }
    }
}
