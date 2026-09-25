package org.onebusaway.vehicletracker.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * `rider/shape_test.go`, case for case and number for number, as `ShapeGeometryTests.swift` ports
 * it: the phone's projection has to agree with the server's (spec goal 5), and these are the cases
 * that pin it. A delta that fails here means the port is wrong, not the delta.
 */
class ShapeGeometryTest {
    /** Runs due north for ~1 km from (47.6000, -122.3300). */
    private val straight = shape(
        GeoPoint(47.6000, -122.3300),
        GeoPoint(47.6045, -122.3300),
        GeoPoint(47.6090, -122.3300),
    )

    /** A square loop: north, east, south, back west to the start. */
    private val loop = shape(
        GeoPoint(47.6000, -122.3300),
        GeoPoint(47.6045, -122.3300), // north 500 m
        GeoPoint(47.6045, -122.3234), // east ~500 m
        GeoPoint(47.6000, -122.3234), // south 500 m
        GeoPoint(47.6000, -122.3300), // west back to start
    )

    private fun shape(vararg points: GeoPoint) = requireNotNull(ShapeGeometry.of(points.toList()))

    @Test fun `distance is the haversine distance`() {
        // 0.009° latitude ≈ 1001 m
        assertEquals(1001.0, Geo.distance(GeoPoint(47.6000, -122.3300), GeoPoint(47.6090, -122.3300)), 5.0)
        assertEquals(0.0, Geo.distance(GeoPoint(1.0, 1.0), GeoPoint(1.0, 1.0)), 0.0)
    }

    @Test fun `initial bearing`() {
        assertEquals(0.0, Geo.initialBearing(GeoPoint(47.6, -122.33), GeoPoint(47.61, -122.33)), 0.5)
        assertEquals(90.0, Geo.initialBearing(GeoPoint(47.6, -122.33), GeoPoint(47.6, -122.32)), 1.0)
        assertEquals(180.0, Geo.initialBearing(GeoPoint(47.61, -122.33), GeoPoint(47.6, -122.33)), 0.5)
    }

    @Test fun `cumulative distances`() {
        assertEquals(3, straight.cumulative.size)
        assertEquals(0.0, straight.cumulative[0], 0.0)
        assertEquals(500.0, straight.cumulative[1], 3.0)
        assertEquals(1001.0, straight.length, 5.0)
        // Where the Go constructor panics, a single point is simply no shape.
        assertNull(ShapeGeometry.of(listOf(GeoPoint(1.0, 1.0))))
    }

    @Test fun `project on the shape`() {
        val p = straight.project(GeoPoint(47.6045, -122.3300), hint = null)
        assertEquals(500.0, p.alongShape, 3.0)
        assertEquals(0.0, p.distanceToShape, 0.5)
        assertEquals(0.0, straight.bearingAt(p.alongShape), 1.0)
    }

    @Test fun `project off the shape`() {
        // 0.001° longitude at 47.6° ≈ 75 m east of the line, 250 m along.
        val p = straight.project(GeoPoint(47.60225, -122.3290), hint = null)
        assertEquals(250.0, p.alongShape, 5.0)
        assertEquals(75.0, p.distanceToShape, 3.0)
    }

    @Test fun `project beyond the ends clamps`() {
        val before = straight.project(GeoPoint(47.5990, -122.3300), hint = null)
        assertEquals(0.0, before.alongShape, 0.01)
        assertEquals(111.0, before.distanceToShape, 3.0)
        val after = straight.project(GeoPoint(47.6100, -122.3300), hint = null)
        assertEquals(straight.length, after.alongShape, 0.01)
    }

    @Test fun `a loop uses the hint`() {
        // The start/end corner is equidistant from the first and last segments.
        val nearStart = GeoPoint(47.6001, -122.3299)
        val first = loop.project(nearStart, hint = null)
        assertTrue("without a hint the global/first minimum wins", first.alongShape < 50.0)

        val last = loop.project(nearStart, hint = loop.length - 30)
        assertTrue("with a late hint the last segment wins", last.alongShape > loop.length - 60)

        // A hint far from any local minimum still returns a valid local minimum.
        val p = loop.project(nearStart, hint = 1000.0)
        assertTrue(p.alongShape < 50 || p.alongShape > loop.length - 60)
    }

    @Test fun `pointAt and bearingAt`() {
        val p = loop.pointAt(250.0)
        assertEquals(47.60225, p.lat, 0.0001)
        assertEquals(-122.3300, p.lon, 0.0001)
        assertEquals(0.0, loop.bearingAt(250.0), 1.0)
        assertEquals(90.0, loop.bearingAt(loop.cumulative[1] + 100), 2.0)
        assertEquals(180.0, loop.bearingAt(loop.cumulative[2] + 100), 1.0)

        // Clamping.
        assertEquals(loop.points[0], loop.pointAt(-5.0))
        assertEquals(loop.points[loop.points.size - 1], loop.pointAt(loop.length + 5))

        // Round trip: pointAt(along) projects back to ~along.
        for (along in listOf(10.0, 400.0, 900.0, 1400.0)) {
            val q = loop.project(loop.pointAt(along), hint = along)
            assertEquals("along=$along", along, q.alongShape, 1.0)
            assertFalse(q.distanceToShape.isNaN())
        }
    }

    @Test fun `a shared loop point is told apart only by the hint`() {
        // The loop starts and ends at this exact point, so both passes are 0 m away and only the
        // hint can tell them apart.
        val shared = GeoPoint(47.6000, -122.3300)

        val unhinted = loop.project(shared, hint = null)
        assertTrue("without a hint the first pass wins", unhinted.alongShape < 1.0)

        val hint = loop.length - 30
        val hinted = loop.project(shared, hint)
        assertTrue("with a late hint the last pass wins", hinted.alongShape > loop.length - 60)

        // Near, but not on, the shared corner: ~5 m from the first pass and ~15 m from the last. A
        // purely proportional band (2*5+1 = 11 m) would exclude the last pass outright; the 30 m
        // candidate band keeps it in play for the hint.
        val nearCorner = GeoPoint(47.6001347, -122.3299334)
        assertTrue(loop.project(nearCorner, hint = null).alongShape < 50.0)
        val offset = loop.project(nearCorner, hint)
        assertTrue("the band must be wide enough to reach the last pass", offset.alongShape > loop.length - 60)
        assertEquals(15.0, offset.distanceToShape, 2.0)
    }

    /**
     * The two legs of an out-and-back run 10 m apart, and just past the turnaround the last match
     * is equally near both. A vehicle moves forward along its trip, so the leg ahead wins; a short
     * step backwards on the same leg still beats the far-ahead pass.
     */
    @Test fun `an out-and-back prefers the forward pass`() {
        val s = shape(
            GeoPoint(47.6000, -122.33000),
            GeoPoint(47.6090, -122.33000), // north 1 km
            GeoPoint(47.6090, -122.32987), // 10 m east
            GeoPoint(47.6000, -122.32987), // back south, parallel
        )

        val turnaround = 1001.0
        var p = s.project(GeoPoint(47.6081, -122.32987), turnaround) // 100 m down the return leg
        assertEquals("the return leg, ahead of the match, not the outbound leg behind it", 1111.0, p.alongShape, 15.0)
        assertTrue(p.distanceToShape < 1.0)

        val halfway = 500.0
        p = s.project(GeoPoint(47.6044, -122.33000), halfway) // 11 m behind the match, same leg
        assertEquals("a small step back on this leg beats the return leg 1 km ahead", 489.0, p.alongShape, 15.0)
    }
}
