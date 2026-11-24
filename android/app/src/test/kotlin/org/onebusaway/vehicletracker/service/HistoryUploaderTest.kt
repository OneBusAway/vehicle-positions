package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FileOfflineFixQueue
import org.onebusaway.vehicletracker.data.MAX_HISTORY_AGE_SEC
import org.onebusaway.vehicletracker.data.OccupancyStatus
import org.onebusaway.vehicletracker.data.reportsWaiting
import org.onebusaway.vehicletracker.data.api.ApiFactory
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

    // POST /api/v1/locations/history refuses a field it does not know with a 400, for which the batch is dropped.
    @Test fun `a history upload leaves out the occupancy its fixes were recorded with`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"saved":2,"invalid":0}"""))
        val queue = FakeOfflineFixQueue()
        queue.add(queuedFix("van-1", timestamp = 1001L).copy(occupancyStatus = OccupancyStatus.FULL))
        queue.add(queuedFix("van-1", timestamp = 1002L))
        val uploader = uploaderFor(ApiFactory { "jwt" }.create(server.url("/").toString()), queue)

        assertEquals(HistoryUpload.BATCH_SENT, uploader.upload(nowEpochSec = 1020L))

        assertEquals(1, server.requestCount)
        val points = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject.getValue("points").jsonArray
        assertEquals(2, points.size)
        points.forEach { assertFalse(it.jsonObject.containsKey("occupancy_status")) }
        server.shutdown()
    }

    // A phone coming out of a dead zone is often on weak 2G, about 2,500 bytes a second up. A batch that can't go inside
    // the call's ten second timeout there is kept and starts over each time, so it never goes. Eight seconds leaves room
    // for headers, long trip ids and a live report on the same link.
    @Test fun `a full batch fits the call's timeout on weak 2G, and the next batch takes the rest`() = runTest {
        val server = MockWebServer().apply { start() }
        repeat(2) { server.enqueue(MockResponse().setResponseCode(201).setBody("""{"saved":80,"invalid":0}""")) }
        val queue = FakeOfflineFixQueue()
        repeat(120) { queue.add(fixAsThePhoneTakesIt(timestamp = 1001L + it)) }
        val uploader = uploaderFor(ApiFactory { "jwt" }.create(server.url("/").toString()), queue)

        assertEquals(HistoryUpload.BATCH_SENT, uploader.upload(nowEpochSec = 1200L))

        assertEquals(1, server.requestCount)
        val full = server.takeRequest()
        assertTrue("${full.bodySize} bytes", full.bodySize <= 8 * 2_500)
        assertEquals((1001L..1080L).toList(), timestampsIn(full))

        assertEquals(HistoryUpload.BATCH_SENT, uploader.upload(nowEpochSec = 1200L))

        assertEquals(2, server.requestCount)
        assertEquals((1081L..1120L).toList(), timestampsIn(server.takeRequest()))
        assertEquals(0, queue.queued.size)
        server.shutdown()
    }

    @Test fun `more fixes too old than a batch holds are all dropped unsent`() = runTest {
        val now = 1_000_000L
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"saved":2,"invalid":0}"""))
        val queue = FakeOfflineFixQueue()
        repeat(600) { queue.add(queuedFix("van-1", timestamp = now - MAX_HISTORY_AGE_SEC - 600 + it)) }
        queue.add(queuedFix("van-1", timestamp = now - 20))
        queue.add(queuedFix("van-1", timestamp = now - 10))
        val uploader = uploaderFor(ApiFactory { "jwt" }.create(server.url("/").toString()), queue)

        assertEquals(HistoryUpload.BATCH_SENT, uploader.upload(nowEpochSec = now))

        assertEquals(1, server.requestCount)
        assertEquals(listOf(now - 20, now - 10), timestampsIn(server.takeRequest()))
        assertEquals(0, queue.queued.size)
        server.shutdown()
    }

    // Android gives the bearing, speed and accuracy as floats, which are long once written as doubles.
    private fun fixAsThePhoneTakesIt(timestamp: Long) = queuedFix("van-1", timestamp).copy(
        tripId = "trip-0830-weekday",
        latitude = -1.2921347,
        longitude = 36.8219462,
        bearing = 182.34567f.toDouble(),
        speed = 8.513333f.toDouble(),
        accuracy = 3.7900925f.toDouble(),
    )

    private fun timestampsIn(upload: RecordedRequest): List<Long> =
        Json.parseToJsonElement(upload.body.readUtf8()).jsonObject.getValue("points").jsonArray
            .map { it.jsonObject.getValue("timestamp").toString().toLong() }

    // What the waiting count says will send is what goes: both ask tooOldToUpload. Read at one fixed moment, so no
    // clock tick between queueing the fixes and uploading them can move one across the limit.
    @Test fun `a fix at the age limit is sent and one a second older dropped, as the count said`() = runTest {
        val now = 1_000_000L
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"saved":1,"invalid":0}"""))
        val queue = FakeOfflineFixQueue()
        queue.add(queuedFix("van-1", timestamp = now - MAX_HISTORY_AGE_SEC - 1))
        queue.add(queuedFix("van-1", timestamp = now - MAX_HISTORY_AGE_SEC))
        val uploader = uploaderFor(ApiFactory { "jwt" }.create(server.url("/").toString()), queue)
        val counted = reportsWaiting(queue.contents.value, signedInDriver, now)?.count

        assertEquals(HistoryUpload.BATCH_SENT, uploader.upload(nowEpochSec = now))

        assertEquals(1, server.requestCount)
        val points = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject.getValue("points").jsonArray
        val sent = points.map { it.jsonObject.getValue("timestamp").toString().toLong() }
        assertEquals("only the fix at the limit", listOf(now - MAX_HISTORY_AGE_SEC), sent)
        assertEquals("the count said as many", points.size, counted)
        assertEquals("the older one dropped, not kept", 0, queue.queued.size)
        server.shutdown()
    }
}
