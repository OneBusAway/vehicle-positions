package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.FollowHeading

class FollowHeadingTest {
    @Test fun `the heading is north until a fix has a course`() {
        val heading = FollowHeading()

        assertEquals(0.0, heading.degrees, 0.0)
        assertEquals(0.0, heading.update(null), 0.0)
    }

    @Test fun `a fix with no course keeps the last heading`() {
        val heading = FollowHeading()
        heading.update(90.0)

        assertEquals(90.0, heading.update(null), 0.0)
    }

    @Test fun `a course out of range keeps the last heading`() {
        val heading = FollowHeading()
        heading.update(90.0)

        // -1 is Core Location's "unknown"; a caller passing it through raw must not steer north.
        assertEquals(90.0, heading.update(-1.0), 0.0)
        assertEquals(90.0, heading.update(360.5), 0.0)
        assertEquals(90.0, heading.update(Double.NaN), 0.0)
    }

    @Test fun `due north is a course, not a missing one`() {
        val heading = FollowHeading()
        heading.update(90.0)

        assertEquals(0.0, heading.update(0.0), 0.0)
        assertEquals(360.0, heading.update(360.0), 0.0)
    }
}
