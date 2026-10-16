package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.followPadding

class FollowPaddingTest {
    /** Where MapLibre puts the vehicle: the middle of what the padding leaves. */
    private fun vehicleY(mapHeight: Int, padding: DoubleArray) = (padding[1] + mapHeight - padding[3]) / 2

    @Test fun `the vehicle sits a third of the way up the map nothing covers`() {
        // A 2000 px map under a 300 px card and above a 400 px bar leaves 1300 px uncovered.
        val padding = followPadding(mapHeight = 2000, coveredTop = 300, coveredBottom = 400)

        assertEquals(300 + 1300 * 2 / 3.0, vehicleY(2000, padding), 1e-9)
        assertEquals(0.0, padding[0], 0.0)
        assertEquals(0.0, padding[2], 0.0)
    }

    @Test fun `with nothing over it, the vehicle sits a third of the way up the whole map`() {
        assertEquals(2000 * 2 / 3.0, vehicleY(2000, followPadding(mapHeight = 2000, coveredTop = 0, coveredBottom = 0)), 1e-9)
    }

    @Test fun `a map covered from edge to edge asks for no negative padding`() {
        val padding = followPadding(mapHeight = 500, coveredTop = 400, coveredBottom = 400)

        assertEquals(400.0, padding[1], 0.0)
        assertEquals(400.0, padding[3], 0.0)
    }
}
