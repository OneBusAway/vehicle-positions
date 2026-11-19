package org.onebusaway.vehicletracker.data

import app.cash.turbine.test
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
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

    /** [count] fixes taken at [NOW], under [owner]. */
    private fun contents(count: Int, owner: String?) = QueueContents(List(count) { NOW }, owner)

    /** The wall clock as the test's virtual time moves it on, starting at [NOW]. */
    private fun TestScope.clock(): () -> Long = { NOW + testScheduler.currentTime / 1000 }

    @Test fun `nothing waits while the queue is empty, whoever it belongs to`() {
        assertNull(reportsWaiting(contents(0, account), signedIn(), NOW))
        assertNull(reportsWaiting(contents(0, offlineQueueAccount(server, "e@example.com")), signedIn(), NOW))
    }

    @Test fun `reports saved under this sign-in will send`() {
        assertEquals(ReportsWaiting(37, WILL_SEND), reportsWaiting(contents(37, account), signedIn(), NOW))
        // Compared as the uploads compare it: the same server and email however they were typed.
        assertEquals(
            ReportsWaiting(1, WILL_SEND),
            reportsWaiting(contents(1, account), signedIn(serverUrl = "$server/", email = " D@Example.com "), NOW),
        )
    }

    // The Signed out card covers the status card meanwhile, and the same driver signing in again keeps the queue.
    @Test fun `an expired sign-in still sends them once the driver signs in again`() {
        assertEquals(ReportsWaiting(4, WILL_SEND), reportsWaiting(contents(4, account), signedIn(token = null), NOW))
    }

    // HistoryUploader answers NOT_SIGNED_IN for them, the backlog job then stops without coming back, and the next
    // sign-in drops them.
    @Test fun `reports saved under another sign-in never send`() {
        assertEquals(
            ReportsWaiting(37, NEVER),
            reportsWaiting(contents(37, offlineQueueAccount(server, "e@example.com")), signedIn(), NOW),
        )
        assertEquals(
            "the same driver on another server",
            ReportsWaiting(37, NEVER),
            reportsWaiting(contents(37, offlineQueueAccount("https://other.example.org", email)), signedIn(), NOW),
        )
        assertEquals(
            "a queue nobody was handed",
            ReportsWaiting(2, NEVER),
            reportsWaiting(contents(2, null), signedIn(), NOW),
        )
    }

    @Test fun `the count follows the queue, from 0 to 1 to many and back to 0`() = runTest {
        val queue = FakeOfflineFixQueue().apply { claimFor(account) }
        val sessions = FakeSessionStore().apply { state.value = signedIn() }

        WaitingReports(queue, sessions, clock()).waiting.test {
            assertNull("none at first", awaitItem())
            queue.add(queuedFix("van-1", timestamp = NOW + 1))
            assertEquals(ReportsWaiting(1, WILL_SEND), awaitItem())
            (2L..37L).forEach { queue.add(queuedFix("van-1", timestamp = NOW + it)) }
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

        assertNull(WaitingReports(unreadable, sessions, clock()).waiting.first())
    }

    // Sign in saves the new driver's session, then hands them the queue, which drops the last driver's fixes.
    @Test fun `another driver's sign-in says they won't send, until the queue drops them`() = runTest {
        val queue = FakeOfflineFixQueue().apply { claimFor(account) }
        val sessions = FakeSessionStore().apply { state.value = signedIn() }
        queue.add(queuedFix("van-1", timestamp = NOW))

        WaitingReports(queue, sessions, clock()).waiting.test {
            assertEquals(ReportsWaiting(1, WILL_SEND), expectMostRecentItem())
            sessions.state.value = signedIn(email = "e@example.com")
            assertEquals(ReportsWaiting(1, NEVER), awaitItem())
            queue.claimFor(offlineQueueAccount(server, "e@example.com"))
            assertNull(awaitItem())
        }
    }

    // HistoryUploader drops what tooOldToUpload says, so a fix the count shows is one it would send. Read at one fixed
    // moment, so no clock tick between taking the fixes and counting them can move them across the limit.
    @Test fun `a fix counts until the uploads would drop it as too old`() {
        val atLimit = NOW - MAX_HISTORY_AGE_SEC
        val pastLimit = atLimit - 1

        assertEquals(
            ReportsWaiting(1, WILL_SEND, queued = 2),
            reportsWaiting(QueueContents(listOf(pastLimit, atLimit), account), signedIn(), NOW),
        )
        assertNull("only too old to send", reportsWaiting(QueueContents(listOf(pastLimit), account), signedIn(), NOW))
        // Older ones wouldn't go under any sign-in, so the sign-in isn't why: they aren't counted as won't send either.
        assertEquals(
            "another sign-in's",
            ReportsWaiting(1, NEVER, queued = 2),
            reportsWaiting(QueueContents(listOf(pastLimit, atLimit), null), signedIn(), NOW),
        )
    }

    // The queue doesn't change as its fixes age, so nothing else would tell a card left on screen.
    @Test fun `the count drops as each fix ages past the limit, with nothing else changing`() = runTest {
        val queue = FakeOfflineFixQueue().apply { claimFor(account) }
        val sessions = FakeSessionStore().apply { state.value = signedIn() }
        queue.add(queuedFix("van-1", timestamp = NOW - MAX_HISTORY_AGE_SEC + 5))
        queue.add(queuedFix("van-1", timestamp = NOW - MAX_HISTORY_AGE_SEC + 60))

        WaitingReports(queue, sessions, clock()).waiting.test {
            assertEquals(ReportsWaiting(2, WILL_SEND), expectMostRecentItem())
            advanceTimeBy(5_999)
            runCurrent()
            expectNoEvents()
            advanceTimeBy(1)
            runCurrent()
            assertEquals("the older aged out, still queued", ReportsWaiting(1, WILL_SEND, queued = 2), awaitItem())
            advanceTimeBy(54_999)
            runCurrent()
            expectNoEvents()
            advanceTimeBy(1)
            runCurrent()
            assertNull("both aged out", awaitItem())
        }
    }

    private companion object {
        /** A wall clock a long way past the epoch, so a fix older than the limit still has a positive time. */
        const val NOW = 1_000_000L
    }
}
