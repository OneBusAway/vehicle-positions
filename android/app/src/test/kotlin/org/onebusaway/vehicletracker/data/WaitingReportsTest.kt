package org.onebusaway.vehicletracker.data

import app.cash.turbine.test
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.WaitingOutlook.NEVER
import org.onebusaway.vehicletracker.data.WaitingOutlook.WILL_SEND
import org.onebusaway.vehicletracker.service.queuedFix

/** The reports waiting on the phone, as the status card and the notification are told them. */
class WaitingReportsTest {
    @get:Rule val tmp = TemporaryFolder()

    private val server = "https://tracker.example.org"
    private val email = "d@example.com"
    private val account = offlineQueueAccount(server, email)

    private fun signedIn(serverUrl: String = server, email: String = this.email, token: String? = "jwt") =
        Session(serverUrl = serverUrl, token = token, issuedAtEpochSec = 0L, email = email)

    @Test fun `nothing waits while the queue is empty, whoever it belongs to`() {
        assertNull(reportsWaiting(QueueContents(0, account), signedIn()))
        assertNull(reportsWaiting(QueueContents(0, offlineQueueAccount(server, "e@example.com")), signedIn()))
    }

    @Test fun `reports saved under this sign-in will send`() {
        assertEquals(ReportsWaiting(37, WILL_SEND), reportsWaiting(QueueContents(37, account), signedIn()))
        // Compared as the uploads compare it: the same server and email however they were typed.
        assertEquals(
            ReportsWaiting(1, WILL_SEND),
            reportsWaiting(QueueContents(1, account), signedIn(serverUrl = "$server/", email = " D@Example.com ")),
        )
    }

    // The Signed out card covers the status card meanwhile, and the same driver signing in again keeps the queue.
    @Test fun `an expired sign-in still sends them once the driver signs in again`() {
        assertEquals(ReportsWaiting(4, WILL_SEND), reportsWaiting(QueueContents(4, account), signedIn(token = null)))
    }

    // HistoryUploader answers NOT_SIGNED_IN for them, the backlog job then stops without coming back, and the next
    // sign-in drops them.
    @Test fun `reports saved under another sign-in never send`() {
        assertEquals(
            ReportsWaiting(37, NEVER),
            reportsWaiting(QueueContents(37, offlineQueueAccount(server, "e@example.com")), signedIn()),
        )
        assertEquals(
            "the same driver on another server",
            ReportsWaiting(37, NEVER),
            reportsWaiting(QueueContents(37, offlineQueueAccount("https://other.example.org", email)), signedIn()),
        )
        assertEquals("a queue nobody was handed", ReportsWaiting(2, NEVER), reportsWaiting(QueueContents(2, null), signedIn()))
    }

    @Test fun `the count follows the queue, from 0 to 1 to many and back to 0`() = runTest {
        val queue = FakeOfflineFixQueue().apply { claimFor(account) }
        val sessions = FakeSessionStore().apply { state.value = signedIn() }

        WaitingReports(queue, sessions).waiting.test {
            assertNull("none at first", awaitItem())
            queue.add(queuedFix("van-1", timestamp = 1L))
            assertEquals(ReportsWaiting(1, WILL_SEND), awaitItem())
            (2L..37L).forEach { queue.add(queuedFix("van-1", timestamp = it)) }
            assertEquals(ReportsWaiting(37, WILL_SEND), expectMostRecentItem())
            queue.remove(queue.oldest(30))
            assertEquals("a batch uploaded", ReportsWaiting(7, WILL_SEND), awaitItem())
            queue.remove(queue.oldest(7))
            assertNull("the rest uploaded", awaitItem())
        }
    }

    // The screen and the notification read it with the rest of what they show, so it must not hold them back.
    @Test fun `a queue that can't be read leaves nothing waiting, rather than nothing shown`() = runTest {
        val unreadable = FileOfflineFixQueue(tmp.newFolder("offline.jsonl"))
        val sessions = FakeSessionStore().apply { state.value = signedIn() }

        assertNull(WaitingReports(unreadable, sessions).waiting.first())
    }

    // Sign in saves the new driver's session, then hands them the queue, which drops the last driver's fixes.
    @Test fun `another driver's sign-in says they won't send, until the queue drops them`() = runTest {
        val queue = FakeOfflineFixQueue().apply { claimFor(account) }
        val sessions = FakeSessionStore().apply { state.value = signedIn() }
        queue.add(queuedFix("van-1"))

        WaitingReports(queue, sessions).waiting.test {
            assertEquals(ReportsWaiting(1, WILL_SEND), expectMostRecentItem())
            sessions.state.value = signedIn(email = "e@example.com")
            assertEquals(ReportsWaiting(1, NEVER), awaitItem())
            queue.claimFor(offlineQueueAccount(server, "e@example.com"))
            assertNull(awaitItem())
        }
    }
}
