package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.supportsMap

class MapSupportTest {
    @Test fun `OpenGL ES 2 cannot draw the map`() {
        assertFalse(supportsMap(0x20000))
        assertFalse("ES 2 with a minor version", supportsMap(0x2FFFF))
        assertFalse("no OpenGL ES reported", supportsMap(0))
    }

    @Test fun `OpenGL ES 3 and later can`() {
        assertTrue(supportsMap(0x30000))
        assertTrue(supportsMap(0x30002))
    }
}
