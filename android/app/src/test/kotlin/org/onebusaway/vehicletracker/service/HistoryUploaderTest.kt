package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.FileOfflineFixQueue
import org.onebusaway.vehicletracker.data.api.LocationHistoryDto
import org.onebusaway.vehicletracker.data.api.TrackerApi

class HistoryUploaderTest {
    @get:Rule val tmp = TemporaryFolder()

    // A full queue makes room for a new fix by evicting its oldest, which may be in the batch on its way. Removing the
    // batch by its size then took the new fix too, though it was never sent.
    @Test fun `a fix added to a full queue during an upload stays queued`() = runTest {
        val api = SlowHistoryApi()
        val started = CompletableDeferred<List<Long>>()
        val watched = object : TrackerApi by api {
            override suspend fun postLocationHistory(body: LocationHistoryDto) {
                started.complete(body.points.map { it.timestamp })
                api.postLocationHistory(body)
            }
        }
        val queue = FileOfflineFixQueue(tmp.newFile(), capacity = 3)
        (1001L..1003L).forEach { queue.add(queuedFix("van-1", timestamp = it)) }
        val uploader = uploaderFor(watched, queue)

        val upload = async { uploader.upload(nowEpochSec = 1020L) }
        assertEquals("the whole queue is on its way", listOf(1001L, 1002L, 1003L), started.await())
        queue.add(queuedFix("van-1", timestamp = 1004L))
        assertEquals("the add evicted 1001", listOf(1002L, 1003L, 1004L), queue.oldest(10).map { it.timestamp })
        api.finishUploads.complete(Unit)

        assertEquals(HistoryUpload.BATCH_SENT, upload.await())
        assertEquals("only the unsent fix is left", listOf(1004L), queue.oldest(10).map { it.timestamp })
    }
}
