package org.onebusaway.vehicletracker.data.map

import org.junit.Assert.assertEquals
import org.junit.Test

class MapFileStateTest {
    @Test fun `a download's percent`() = assertEquals(40, downloadPercent(40, 100))

    @Test fun `rounds down`() = assertEquals(33, downloadPercent(1, 3))

    @Test fun `never says 100 before the file is in place`() = assertEquals(99, downloadPercent(100, 100))

    @Test fun `an unknown total shows nothing done`() = assertEquals(0, downloadPercent(5_000, -1))

    @Test fun `a metro-sized file`() = assertEquals(50, downloadPercent(46_500_000, 93_000_000))
}
