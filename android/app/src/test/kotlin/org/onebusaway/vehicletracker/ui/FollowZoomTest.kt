package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.MAX_FOLLOW_ZOOM
import org.onebusaway.vehicletracker.ui.map.MIN_FOLLOW_ZOOM
import org.onebusaway.vehicletracker.ui.map.ROAD_AHEAD_M
import org.onebusaway.vehicletracker.ui.map.followZoom
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.pow

class FollowZoomTest {
    private val seattle = 47.6

    /** MapLibre's own scale: metres across one pixel at [zoom], with 512 px tiles. */
    private fun metresPerDp(latitude: Double, zoom: Double): Double =
        2 * PI * 6_378_137.0 * cos(latitude * PI / 180) / (512 * 2.0.pow(zoom))

    @Test fun `the zoom puts the road ahead across the pixels ahead`() {
        for (latitude in listOf(seattle, -1.29, 28.6)) {
            val zoom = followZoom(latitude, aheadDp = 300.0)

            assertEquals(ROAD_AHEAD_M, 300.0 * metresPerDp(latitude, zoom), 0.01 * ROAD_AHEAD_M)
        }
    }

    @Test fun `the equator needs a closer zoom than Seattle for the same road`() {
        // cos(47.6°) is 0.674, and log2(1 / 0.674) is 0.57.
        assertEquals(0.57, followZoom(0.0, aheadDp = 300.0) - followZoom(seattle, aheadDp = 300.0), 0.01)
    }

    @Test fun `twice the room on screen is one zoom level closer`() {
        assertEquals(1.0, followZoom(seattle, aheadDp = 600.0) - followZoom(seattle, aheadDp = 300.0), 1e-9)
    }

    @Test fun `the zoom stays within what the map can follow at`() {
        assertEquals(MAX_FOLLOW_ZOOM, followZoom(seattle, aheadDp = 100_000.0), 0.0)
        assertEquals("a map not yet measured", MIN_FOLLOW_ZOOM, followZoom(seattle, aheadDp = 0.0), 0.0)
        assertEquals("the pole", MIN_FOLLOW_ZOOM, followZoom(90.0, aheadDp = 300.0), 0.0)
    }
}
