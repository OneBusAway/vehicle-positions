package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FileOfflineFixQueue
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.issuedAt
import org.onebusaway.vehicletracker.data.offlineQueueAccount
import org.onebusaway.vehicletracker.data.api.LoginResponse
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

class BacklogDrainTest {
    @get:Rule val tmp = TemporaryFolder()

    private val trip = ActiveTrip(7L, "trip-0830", "van-1", "5", "20260804", 100L)

    private fun httpError(code: Int) = HttpException(Response.error<Unit>(code, """{"error":"x"}""".toResponseBody()))

    private suspend fun queueOf(vararg vehicleIds: String, queue: OfflineFixQueue = FakeOfflineFixQueue()) =
        queue.apply { vehicleIds.forEach { add(queuedFix(it)) } }

    private fun drainWith(uploader: HistoryUploader) = BacklogDrain(uploader, clock = { 1020L })

    /** A reporter sharing [uploader] with the job, as Hilt's singleton has it. */
    private fun reporterSharing(
        api: SlowHistoryApi,
        queue: OfflineFixQueue,
        uploader: HistoryUploader,
        scheduler: BacklogUploadScheduler = FakeBacklogUploadScheduler(),
    ): TripReporter {
        var now = 0L
        return TripReporter(TrackerApiProvider { api }, TrackingRepository(), { now += REPORT_INTERVAL_MS; now }, queue, uploader, scheduler, FakeTripStateStore())
    }

    private fun fix(ts: Long) = LocationFix(-1.29, 36.82, bearing = 180.0, speed = 8.5, accuracy = 12.0, timeEpochSec = ts)

    // Each upload reads the batch at the front of the queue and removes it once it has gone, so two at once would send
    // the same batch twice.
    @Test fun `a report that goes through while the job uploads starts no upload, and every fix goes once`() = runTest {
        val api = SlowHistoryApi()
        val queue = queueOf("van-1", "van-2", "van-3")
        val uploader = uploaderFor(api, queue)
        val reporter = reporterSharing(api, queue, uploader)

        val job = async { drainWith(uploader).run() }
        runCurrent()
        assertEquals("the job's upload is on its way", listOf("van-1"), api.uploads)

        val report = launch { reporter.report(trip, fix(ts = 1020L)) }
        runCurrent()
        assertTrue("the report did not wait for the job's upload", report.isCompleted)
        assertEquals(1, api.liveReports)
        assertEquals(listOf("van-1"), api.uploads)

        api.finishUploads.complete(Unit)
        assertFalse(job.await())
        assertEquals(listOf("van-1", "van-2", "van-3"), api.uploads)
        assertEquals(0, queue.oldest(10).size)
    }

    @Test fun `the job waits for the reporter's upload and then sends the next batch`() = runTest {
        val api = SlowHistoryApi()
        val queue = queueOf("van-1", "van-2")
        val uploader = uploaderFor(api, queue)
        val reporter = reporterSharing(api, queue, uploader)

        val report = launch { reporter.report(trip, fix(ts = 1020L)) }
        runCurrent()
        assertEquals("the reporter's upload is on its way", listOf("van-1"), api.uploads)

        val job = async { drainWith(uploader).run() }
        runCurrent()
        assertEquals("the job waits", listOf("van-1"), api.uploads)

        api.finishUploads.complete(Unit)
        report.join()
        assertFalse(job.await())
        assertEquals(listOf("van-1", "van-2"), api.uploads)
        assertEquals(0, queue.oldest(10).size)
    }

    // Scheduling over a running job stops it part way through the queue.
    @Test fun `a report while the job is running does not schedule over it, and one after it has finished does`() = runTest {
        val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
        val queue = queueOf("van-1", "van-2", "van-3", "van-4")
        val uploader = uploaderFor(api, queue)
        val scheduler = FakeBacklogUploadScheduler()
        val reporter = reporterSharing(api, queue, uploader, scheduler)

        val job = async { drainWith(uploader).run() }
        runCurrent()
        assertEquals("the job is between batches", listOf("van-1"), api.uploads)

        reporter.report(trip, fix(ts = 1020L))
        assertEquals("the reporter's batch went, with more queued behind it", listOf("van-1", "van-2"), api.uploads)
        assertEquals(2, queue.oldest(10).size)
        assertEquals(0, scheduler.scheduledAfresh)

        assertFalse(job.await())
        queueOf("van-5", "van-6", queue = queue)
        reporter.report(trip, fix(ts = 1030L))
        assertEquals(1, scheduler.scheduledAfresh)
    }

    // JobScheduler stops the job when the connection goes, and cancels it there.
    @Test fun `a job stopped part way is no longer running`() = runTest {
        val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
        val uploader = uploaderFor(api, queueOf("van-1", "van-2"))

        val job = launch { drainWith(uploader).run() }
        runCurrent()
        assertTrue(uploader.jobRunning)

        job.cancelAndJoin()
        assertFalse(uploader.jobRunning)
    }

    @Test fun `the job sends the whole backlog, a batch every five seconds`() = runTest {
        val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
        val queue = queueOf("van-1", "van-2", "van-3")
        val job = async { drainWith(uploaderFor(api, queue)).run() }

        runCurrent()
        assertEquals(listOf("van-1"), api.uploads)
        advanceTimeBy(HISTORY_UPLOAD_INTERVAL_MS - 1)
        runCurrent()
        assertEquals("not before five seconds", listOf("van-1"), api.uploads)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(listOf("van-1", "van-2"), api.uploads)
        advanceTimeBy(HISTORY_UPLOAD_INTERVAL_MS)
        runCurrent()
        assertEquals(listOf("van-1", "van-2", "van-3"), api.uploads)

        assertFalse("nothing left to try again", job.await())
        assertEquals(0, queue.oldest(10).size)
    }

    @Test fun `the job stops and keeps the queue on a failure for now, to try again later`() = runTest {
        for (failure in listOf(IOException("unreachable"), httpError(401), httpError(429), httpError(500), httpError(503))) {
            val api = SlowHistoryApi().apply {
                finishUploads.complete(Unit)
                historyFailure = { upload -> failure.takeIf { upload == 1 } }
            }
            val queue = queueOf("van-1", "van-2", "van-3")

            val tryAgain = drainWith(uploaderFor(api, queue)).run()

            assertTrue("$failure", tryAgain)
            assertEquals("$failure", listOf("van-1", "van-2"), api.uploads)
            assertEquals("$failure", listOf("van-2", "van-3"), queue.oldest(10).map { it.vehicleId })
        }
    }

    @Test fun `the job drops a batch refused for good and goes on`() = runTest {
        for (code in listOf(400, 403, 404)) {
            val api = SlowHistoryApi().apply {
                finishUploads.complete(Unit)
                historyFailure = { upload -> httpError(code).takeIf { upload == 0 } }
            }
            val queue = queueOf("van-1", "van-2")

            val tryAgain = drainWith(uploaderFor(api, queue)).run()

            assertFalse("HTTP $code", tryAgain)
            assertEquals("HTTP $code", listOf("van-1", "van-2"), api.uploads)
            assertEquals("HTTP $code", 0, queue.oldest(10).size)
        }
    }

    // The fixes would go with whatever token the phone holds, so with nobody signed in, or someone else, they wait.
    @Test fun `with nobody signed in to the queue's account the job uploads nothing and keeps the queue`() = runTest {
        for ((label, session) in listOf(
            "signed out" to signedInDriver.copy(token = null, refreshToken = null, email = null),
            "session ended" to signedInDriver.copy(token = null, refreshToken = null),
            "another driver" to signedInDriver.copy(email = "other@example.com"),
            "another server" to signedInDriver.copy(serverUrl = "https://other.example.org/"),
        )) {
            val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
            val queue = queueOf("van-1", queue = FileOfflineFixQueue(tmp.newFile()))
            val sessions = FakeSessionStore().apply { state.value = session }

            val tryAgain = drainWith(uploaderFor(api, queue, sessions)).run()

            assertFalse(label, tryAgain)
            assertEquals(label, emptyList<String>(), api.uploads)
            assertEquals(label, 1, queue.oldest(10).size)
        }
    }

    @Test fun `the job uploads the fixes of the driver who owns the queue`() = runTest {
        val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
        val queue = queueOf("van-1", queue = FileOfflineFixQueue(tmp.newFile()))

        assertFalse(drainWith(uploaderFor(api, queue)).run())

        assertEquals(listOf("van-1"), api.uploads)
        assertEquals(0, queue.oldest(10).size)
    }

    // Sign in saves the new session, then drops the last driver's fixes: between the two, the token is the new driver's.
    @Test fun `the job sends nothing between a new driver's sign-in and the queue being handed to them`() = runTest {
        val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
        val queue = queueOf("van-1", queue = FileOfflineFixQueue(tmp.newFile()))
        val sessions = FakeSessionStore().apply { state.value = signedInDriver }
        val drain = drainWith(uploaderFor(api, queue, sessions))
        sessions.saveLogin(
            "http://localhost/",
            "next@example.com",
            LoginResponse(accessToken = "next", refreshToken = "next-refresh", expiresIn = 900).issuedAt(1020L),
        )

        assertFalse(drain.run())
        assertEquals(emptyList<String>(), api.uploads)

        queue.claimFor(offlineQueueAccount("http://localhost/", "next@example.com"))
        assertFalse(drain.run())
        assertEquals(emptyList<String>(), api.uploads)
    }

    /** A queue whose file fails: the calls switched on throw. */
    private class FailingQueue(val inner: FakeOfflineFixQueue = FakeOfflineFixQueue()) : OfflineFixQueue by inner {
        var oldestFails = false
        var removeFails = false

        override suspend fun oldest(max: Int): List<LocationReportDto> {
            if (oldestFails) throw IOException("I/O error")
            return inner.oldest(max)
        }

        override suspend fun remove(fixes: List<LocationReportDto>) {
            if (removeFails) throw IOException("No space left on device")
            inner.remove(fixes)
        }
    }

    @Test fun `a queue file that fails ends the job without a crash or a retry`() = runTest {
        for (label in listOf("read", "update")) {
            val api = SlowHistoryApi().apply { finishUploads.complete(Unit) }
            val queue = FailingQueue().apply {
                add(queuedFix("van-1"))
                oldestFails = label == "read"
                removeFails = label == "update"
            }

            assertFalse(label, drainWith(uploaderFor(api, queue)).run())
            assertEquals(label, 1, queue.inner.queued.size)
        }
    }
}
