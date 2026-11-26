package org.onebusaway.vehicletracker.data

import app.cash.turbine.test
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.service.queuedFix

/** [FileOfflineFixQueue.contents]: when the queued fixes were taken, and whose they are, read as a flow. */
class FileOfflineFixQueueContentsTest {
    private val driver = offlineQueueAccount("https://vp.example", "a@example.com")
    private val driverB = offlineQueueAccount("https://vp.example", "b@example.com")

    @get:Rule val tmp = TemporaryFolder()

    @Test fun `the first collection reads what an earlier queue left in the file`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        FileOfflineFixQueue(file).apply {
            claimFor(driver)
            add(queuedFix("van-1", timestamp = 1L))
            add(queuedFix("van-1", timestamp = 2L))
        }

        assertEquals(QueueContents(listOf(1L, 2L), driver), FileOfflineFixQueue(file).contents.first())
    }

    @Test fun `it follows each add, removal and claim`() = runTest {
        val queue = FileOfflineFixQueue(tmp.newFile("offline.jsonl"), capacity = 3)

        queue.contents.test {
            assertEquals(QueueContents(emptyList(), null), awaitItem())
            queue.add(queuedFix("van-1", timestamp = 1L))
            assertEquals(QueueContents(listOf(1L), null), awaitItem())
            queue.claimIfUnowned(driver)
            assertEquals(QueueContents(listOf(1L), driver), awaitItem())
            (2L..4L).forEach { queue.add(queuedFix("van-1", timestamp = it)) }
            assertEquals(
                "a full queue keeps its capacity",
                QueueContents(listOf(2L, 3L, 4L), driver),
                expectMostRecentItem(),
            )
            queue.remove(queue.oldest(2))
            assertEquals(QueueContents(listOf(4L), driver), awaitItem())
            queue.claimFor(driverB)
            assertEquals(
                "another driver's sign-in drops the fixes",
                QueueContents(emptyList(), driverB),
                awaitItem(),
            )
        }
    }

    @Test fun `an owner recorded before the fixes were read shows once they are`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        FileOfflineFixQueue(file).add(queuedFix("van-1"))
        val queue = FileOfflineFixQueue(file)

        queue.claimIfUnowned(driver)

        assertEquals(QueueContents(listOf(1000L), driver), queue.contents.first())
    }

    @Test fun `a queue that can't be read shows nothing waiting, rather than failing whoever reads it`() = runTest {
        val queue = FileOfflineFixQueue(tmp.newFolder("offline.jsonl"))

        queue.contents.test { expectNoEvents() }
    }
}
