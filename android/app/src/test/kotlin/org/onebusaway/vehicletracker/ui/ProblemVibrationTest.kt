package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TripSettings
import org.onebusaway.vehicletracker.ui.tracking.vibratesFor
import org.onebusaway.vehicletracker.ui.tracking.vibrationOn

/** Which changes of problem vibrate the phone, as they tap the wrist on iOS. */
class ProblemVibrationTest {
    private val problems = TrackingProblem.entries - TrackingProblem.NONE

    @Test fun `a problem that appears vibrates, whichever it is`() {
        for (problem in problems) assertTrue("$problem", vibratesFor(TrackingProblem.NONE, problem))
    }

    @Test fun `a problem that gives way to another vibrates`() {
        for (before in problems) for (problem in problems) assertEquals("$before to $problem", before != problem, vibratesFor(before, problem))
    }

    @Test fun `a problem that clears does not`() {
        for (before in TrackingProblem.entries) assertFalse("$before", vibratesFor(before, TrackingProblem.NONE))
    }

    @Test fun `it is on only once the choice has been read`() {
        assertFalse("not read yet", vibrationOn(null))
        assertFalse(vibrationOn(TripSettings(vibrates = false)))
        assertTrue(vibrationOn(TripSettings(vibrates = true)))
    }
}
