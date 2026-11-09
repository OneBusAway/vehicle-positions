package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeFalse
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import java.io.File

class FileOfflineFixQueueTest {
    @get:Rule val tmp = TemporaryFolder()

    private fun report(ts: Long) = LocationReportDto(
        vehicleId = "bus-1", tripId = "T1", routeId = "R1", startDate = "20260804",
        latitude = 1.0, longitude = 2.0, timestamp = ts,
    )

    @Test fun `queued fixes survive a new queue over the same file`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        val first = FileOfflineFixQueue(file)
        first.add(report(1))
        first.add(report(2))
        first.add(report(3))
        first.remove(listOf(report(1)))

        val reopened = FileOfflineFixQueue(file)
        assertEquals(listOf(2L, 3L), reopened.oldest(10).map { it.timestamp })
    }

    @Test fun `over capacity the oldest fixes go first`() = runTest {
        val queue = FileOfflineFixQueue(tmp.newFile("offline.jsonl"), capacity = 3)
        (1L..5L).forEach { queue.add(report(it)) }
        assertEquals(listOf(3L, 4L, 5L), queue.oldest(10).map { it.timestamp })
    }

    @Test fun `a line cut short by a crash is skipped`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        FileOfflineFixQueue(file).add(report(1))
        file.appendText("""{"vehicle_id":"bus-1","lati""")
        assertEquals(listOf(1L), FileOfflineFixQueue(file).oldest(10).map { it.timestamp })
    }

    @Test fun `a fix added after a cut-short line survives a reopen`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        FileOfflineFixQueue(file).add(report(1))
        file.appendText("""{"vehicle_id":"bus-1","lati""")
        FileOfflineFixQueue(file).add(report(2))
        assertEquals(listOf(1L, 2L), FileOfflineFixQueue(file).oldest(10).map { it.timestamp })
    }

    @Test fun `claiming for another account drops the queue, the same account keeps it`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        val queue = FileOfflineFixQueue(file)
        queue.claimFor("https://vp.example a@example.com")
        queue.add(report(1))
        queue.claimFor("https://vp.example a@example.com")
        assertEquals(listOf(1L), queue.oldest(10).map { it.timestamp })

        val reopened = FileOfflineFixQueue(file)
        reopened.claimFor("https://vp.example b@example.com")
        assertEquals(emptyList<Long>(), reopened.oldest(10).map { it.timestamp })
        assertEquals(emptyList<Long>(), FileOfflineFixQueue(file).oldest(10).map { it.timestamp })
    }

    // A driver who signed in on a build without the queue was never handed it, so it has no owner file.
    private val driverA = "https://vp.example a@example.com"
    private val driverB = "https://vp.example b@example.com"

    @Test fun `an unowned queue claimed for the restored session keeps its fixes at that driver's sign-in`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        FileOfflineFixQueue(file).add(report(1))

        FileOfflineFixQueue(file).claimIfUnowned(driverA)
        val atSignIn = FileOfflineFixQueue(file)
        atSignIn.claimFor(driverA)

        assertEquals(listOf(1L), atSignIn.oldest(10).map { it.timestamp })
    }

    @Test fun `another account signing in still drops what the restored session claimed`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        FileOfflineFixQueue(file).add(report(1))

        FileOfflineFixQueue(file).claimIfUnowned(driverA)
        val atSignIn = FileOfflineFixQueue(file)
        atSignIn.claimFor(driverB)

        assertEquals(emptyList<Long>(), atSignIn.oldest(10).map { it.timestamp })
    }

    @Test fun `a queue that has an owner keeps it`() = runTest {
        val file = tmp.newFile("offline.jsonl")
        val queue = FileOfflineFixQueue(file)
        queue.claimFor(driverA)
        queue.add(report(1))

        queue.claimIfUnowned(driverB)
        assertEquals("nothing is dropped", listOf(1L), queue.oldest(10).map { it.timestamp })
        val atSignIn = FileOfflineFixQueue(file)
        atSignIn.claimFor(driverA)

        assertEquals(listOf(1L), atSignIn.oldest(10).map { it.timestamp })
    }

    @Test fun `an owner that can't be written is logged, not thrown`() = runTest {
        val dir = tmp.newFolder("queue")
        val queue = FileOfflineFixQueue(File(dir, "offline.jsonl"))
        queue.add(report(1))
        dir.setWritable(false)
        try {
            assumeFalse("the folder is still writable, as it is for root", dir.canWrite())
            queue.claimIfUnowned(driverA)
        } finally {
            dir.setWritable(true)
        }

        assertEquals(listOf(1L), queue.oldest(10).map { it.timestamp })
    }
}
