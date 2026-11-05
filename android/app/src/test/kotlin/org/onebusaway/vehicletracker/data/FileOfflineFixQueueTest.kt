package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.api.LocationReportDto

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
        first.removeOldest(1)

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
}
