package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.MAX_HISTORY_AGE_SEC
import org.onebusaway.vehicletracker.data.QueueContents
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.WaitingOutlook.NEVER
import org.onebusaway.vehicletracker.data.WaitingOutlook.WILL_SEND
import org.onebusaway.vehicletracker.data.offlineQueueAccount
import org.onebusaway.vehicletracker.data.reportsWaiting
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

    // Offline for days, each report adds one and the next fix to age out takes one away: speaking each dip would be
    // every report again (#463).
    @Test fun `it stays quiet as a fix ages out on its own`() {
        assertFalse(speaksWaiting(ReportsWaiting(37, WILL_SEND, queued = 40), ReportsWaiting(36, WILL_SEND, queued = 40)))
        assertFalse(speaksWaiting(ReportsWaiting(37, NEVER, queued = 40), ReportsWaiting(36, NEVER, queued = 40)))
        assertFalse(
            "one aged out as the next report came",
            speaksWaiting(ReportsWaiting(37, WILL_SEND, queued = 40), ReportsWaiting(37, WILL_SEND, queued = 41)),
        )
        assertFalse(
            "only too old ones dropped",
            speaksWaiting(ReportsWaiting(37, WILL_SEND, queued = 40), ReportsWaiting(37, WILL_SEND, queued = 37)),
        )
        assertFalse(
            "one aged out and one came, seen at once",
            speaksWaiting(ReportsWaiting(37, WILL_SEND, queued = 40), ReportsWaiting(36, WILL_SEND, queued = 41)),
        )
    }

    @Test fun `it still speaks as an upload sends fixes, with too old ones still queued`() {
        assertTrue(speaksWaiting(ReportsWaiting(37, WILL_SEND, queued = 40), ReportsWaiting(7, WILL_SEND, queued = 10)))
        assertTrue(
            "the too old ones dropped with the batch",
            speaksWaiting(ReportsWaiting(37, WILL_SEND, queued = 40), ReportsWaiting(7, WILL_SEND, queued = 7)),
        )
    }

    // The same, as the queue's contents and the clock give them.
    @Test fun `an age out keeps quiet and an upload speaks, counted from the queue`() {
        val account = offlineQueueAccount("https://tracker.example.org", "d@example.com")
        val session = Session(serverUrl = "https://tracker.example.org", token = "jwt", issuedAtEpochSec = 0L, email = "d@example.com")
        val now = 1_000_000L
        val queued = QueueContents(listOf(now - MAX_HISTORY_AGE_SEC - 1, now - MAX_HISTORY_AGE_SEC, now, now), account)
        val before = reportsWaiting(queued, session, now)!!

        assertFalse("a second on, one aged out", speaksWaiting(before, reportsWaiting(queued, session, now + 1)!!))
        val afterUpload = QueueContents(queued.timestamps.drop(3), account)
        assertTrue("an upload sent two and dropped the one too old", speaksWaiting(before, reportsWaiting(afterUpload, session, now)!!))
    }

    @Test fun `it speaks as the reports turn between will send and won't send`() {
        assertTrue(speaksWaiting(ReportsWaiting(37, WILL_SEND), ReportsWaiting(37, NEVER)))
        assertTrue(speaksWaiting(ReportsWaiting(37, NEVER), ReportsWaiting(38, WILL_SEND)))
    }
}
