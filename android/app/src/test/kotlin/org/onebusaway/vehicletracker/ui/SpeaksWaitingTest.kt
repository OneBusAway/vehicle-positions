package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.WaitingOutlook.NEVER
import org.onebusaway.vehicletracker.data.WaitingOutlook.WILL_SEND
import org.onebusaway.vehicletracker.ui.tracking.speaksWaiting

/** When TalkBack reads the status card's waiting line out. */
class SpeaksWaitingTest {
    @Test fun `it speaks as the line comes`() {
        assertTrue(speaksWaiting(null, ReportsWaiting(1, WILL_SEND)))
        assertTrue(speaksWaiting(null, ReportsWaiting(37, NEVER)))
    }

    // Every report queues one while there is no connection: reading each out would be every report.
    @Test fun `it stays quiet as each report adds one, or nothing changes`() {
        assertFalse(speaksWaiting(ReportsWaiting(1, WILL_SEND), ReportsWaiting(2, WILL_SEND)))
        assertFalse(speaksWaiting(ReportsWaiting(36, WILL_SEND), ReportsWaiting(37, WILL_SEND)))
        assertFalse(speaksWaiting(ReportsWaiting(37, WILL_SEND), ReportsWaiting(37, WILL_SEND)))
        assertFalse(speaksWaiting(ReportsWaiting(3, NEVER), ReportsWaiting(4, NEVER)))
    }

    @Test fun `it speaks as the count drops while the backlog uploads`() {
        assertTrue(speaksWaiting(ReportsWaiting(1200, WILL_SEND), ReportsWaiting(700, WILL_SEND)))
        assertTrue(speaksWaiting(ReportsWaiting(2, WILL_SEND), ReportsWaiting(1, WILL_SEND)))
    }

    @Test fun `it speaks as the reports turn between will send and won't send`() {
        assertTrue(speaksWaiting(ReportsWaiting(37, WILL_SEND), ReportsWaiting(37, NEVER)))
        assertTrue(speaksWaiting(ReportsWaiting(37, NEVER), ReportsWaiting(38, WILL_SEND)))
    }
}
