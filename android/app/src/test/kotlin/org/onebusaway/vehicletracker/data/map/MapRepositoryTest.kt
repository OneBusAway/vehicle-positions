package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * [MapRepository]'s decisions, on virtual time, with the server and the file's host faked and the
 * store writing to a real directory.
 */
class MapRepositoryTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val url = "https://maps.example.org/agency.pmtiles"
    private val v1 = FileVersion(etag = "\"v1\"")
    private val v2 = FileVersion(etag = "\"v2\"")
    private var clock = 1_790_000_000L
    private val day = CHECK_EVERY_S

    private lateinit var dir: File
    private lateinit var store: MapFileStore
    private val server = FakeMapServer()
    private val fetcher = FakeFetcher()

    private fun TestScope.setUp() {
        dir = tempFolder.newFolder("map")
        store = MapFileStore(dir, io = StandardTestDispatcher(testScheduler))
        server.now = { testScheduler.currentTime }
    }

    /**
     * Runs everything due in the next second. The repository works in [TestScope.backgroundScope],
     * which advanceUntilIdle does not wait for; retries come later than this, so a test that
     * wants one advances the clock itself.
     */
    private fun TestScope.settle() {
        advanceTimeBy(1_000)
        runCurrent()
    }

    /** A repository starting up, as at app start, with its first look at the directory done. */
    private fun TestScope.started(): MapRepository =
        MapRepository(server, store, fetcher, backgroundScope, now = { clock }).also { settle() }

    private fun TestScope.statesOf(repository: MapRepository): List<MapFileState> {
        val states = mutableListOf<MapFileState>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.state.collect { states += it } }
        return states
    }

    /** A map that has been here since [checkedAt], its file on disk. */
    private suspend fun mapHere(name: String = "map-a.pmtiles", checkedAt: Long = clock, from: String = url): File {
        store.save(MapRecord(current = StoredMap(from, name, v1, checkedAt)))
        return File(dir, name).apply { writeText("map") }
    }

    @Test fun `a map from before is shown at start, and files nothing names are deleted`() = runTest {
        setUp()
        val map = mapHere()
        File(dir, "map-old.pmtiles").writeText("old")
        File(dir, "map-x.pmtiles.part").writeText("abandoned")

        val repository = started()

        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals(setOf("map.json", "map-a.pmtiles"), dir.list()!!.toSet())
    }

    @Test fun `a recorded map whose file is gone is forgotten`() = runTest {
        setUp()
        store.save(MapRecord(current = StoredMap(url, "map-a.pmtiles", v1, clock)))

        val repository = started()

        assertEquals(MapFileState.None, repository.state.value)
        assertNull(store.load().current)
    }

    @Test fun `the first download shows its progress, then the map`() = runTest {
        setUp()
        val repository = started()
        val states = statesOf(repository)
        fetcher.steps += fetcher.complete(v1, progress = listOf(25L to 100L, 26L to 100L, 60L to 100L))

        repository.refresh()
        settle()

        val target = fetcher.requests.single().target
        assertEquals(
            listOf(
                MapFileState.None,
                MapFileState.Downloading(25, 100),
                MapFileState.Downloading(26, 100),
                MapFileState.Downloading(60, 100),
                MapFileState.Ready(target),
            ),
            states,
        )
        assertEquals(MapRecord(current = StoredMap(url, target.name, v1, clock)), store.load())
    }

    @Test fun `progress is shown once per percent, not once per read`() = runTest {
        setUp()
        val repository = started()
        val states = statesOf(repository)
        fetcher.steps += fetcher.complete(v1, progress = listOf(1_000L to 1_000_000L, 2_000L to 1_000_000L, 10_000L to 1_000_000L))

        repository.refresh()
        settle()

        assertEquals(listOf(MapFileState.Downloading(1_000, 1_000_000), MapFileState.Downloading(10_000, 1_000_000)), states.filterIsInstance<MapFileState.Downloading>())
    }

    @Test fun `no map from the server shows the plain map and keeps the file until the next start`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        server.answers += MapServerAnswer.NoMap
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(MapFileState.None, repository.state.value)
        assertEquals(MapRecord(), store.load())
        assertTrue("a map on screen may still be reading it", map.exists())

        started()

        assertFalse("gone at the next start", map.exists())
    }

    @Test fun `a check within a day asks nobody`() = runTest {
        setUp()
        mapHere(checkedAt = clock - day + 60)
        val repository = started()

        repository.refresh()
        settle()

        assertTrue(server.askedAt.isEmpty())
        assertTrue(fetcher.requests.isEmpty())
    }

    @Test fun `after a day the host is asked whether the map changed`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        fetcher.steps += fetcher.unchanged()
        val repository = started()

        repository.refresh()
        settle()

        val request = fetcher.requests.single()
        assertEquals(v1, request.knownVersion)
        assertNull(request.partVersion)
        assertEquals(clock, store.load().current!!.checkedAtEpochSec)
        assertEquals(MapFileState.Ready(map), repository.state.value)
    }

    @Test fun `a clock set back makes the check due`() = runTest {
        setUp()
        mapHere(checkedAt = clock + 3_600)
        fetcher.steps += fetcher.unchanged()
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(1, fetcher.requests.size)
    }

    @Test fun `a newer version downloads under a new name, and the old file waits for the next start`() = runTest {
        setUp()
        val old = mapHere(checkedAt = clock - day)
        fetcher.steps += fetcher.complete(v2)
        val repository = started()

        repository.refresh()
        settle()

        val new = fetcher.requests.single().target
        assertNotEquals(old, new)
        assertEquals(MapFileState.Ready(new), repository.state.value)
        assertEquals(StoredMap(url, new.name, v2, clock), store.load().current)
        assertTrue("an open map may be reading it", old.exists())

        started()

        assertFalse(old.exists())
        assertTrue(new.exists())
    }

    @Test fun `an update downloads without the progress note`() = runTest {
        setUp()
        mapHere(checkedAt = clock - day)
        fetcher.steps += fetcher.complete(v2, progress = listOf(10L to 100L, 90L to 100L))
        val repository = started()
        val states = statesOf(repository)

        repository.refresh()
        settle()

        assertTrue("states were $states", states.all { it is MapFileState.Ready })
    }

    @Test fun `a new URL from the server downloads a new map`() = runTest {
        setUp()
        mapHere(checkedAt = clock - day, from = "https://old.example.org/a.pmtiles")
        fetcher.steps += fetcher.complete(v2)
        val repository = started()

        repository.refresh()
        settle()

        val request = fetcher.requests.single()
        assertEquals(url, request.url)
        assertNull("the old URL's version says nothing about the new one", request.knownVersion)
        assertEquals(url, store.load().current!!.url)
    }

    @Test fun `one refresh at a time`() = runTest {
        setUp()
        val repository = started()
        val gate = CompletableDeferred<Unit>()
        server.gate = gate
        server.answers += MapServerAnswer.NoMap

        repository.refresh()
        runCurrent()
        repository.refresh()
        repository.refresh()
        gate.complete(Unit)
        settle()

        assertEquals(1, server.askedAt.size)
    }

    @Test fun `a refresh after the last one finished runs again`() = runTest {
        setUp()
        val repository = started()
        server.answers += MapServerAnswer.NoMap
        server.answers += MapServerAnswer.NoMap

        repository.refresh()
        settle()
        repository.refresh()
        settle()

        assertEquals(2, server.askedAt.size)
    }

    @Test fun `failures before the first copy are retried after 5 s, doubling up to 5 min`() = runTest {
        setUp()
        server.default = MapServerAnswer.Unreachable
        val repository = started()
        val start = testScheduler.currentTime

        repository.refresh()
        advanceTimeBy(25 * 60_000L)

        val gaps = server.askedAt.map { it - start }.zipWithNext { a, b -> b - a }.take(8)
        assertEquals(listOf(5_000L, 10_000L, 20_000L, 40_000L, 80_000L, 160_000L, 300_000L, 300_000L), gaps)
        assertEquals(MapFileState.Failed(MapFailure.NETWORK), repository.state.value)
    }

    @Test fun `with a map here, a failed check waits for the next refresh`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        server.default = MapServerAnswer.Unreachable
        val repository = started()

        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(1, server.askedAt.size)
        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals("a network failure never forgets the map", map.name, store.load().current?.fileName)
    }

    @Test fun `a refused token stops until the next refresh`() = runTest {
        setUp()
        server.default = MapServerAnswer.SignedOut
        val repository = started()

        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(1, server.askedAt.size)
        assertEquals(MapFileState.None, repository.state.value)
    }

    @Test fun `a broken file is not fetched again within a day, then only if it changed`() = runTest {
        setUp()
        fetcher.steps += fetcher.fail(MapFailure.INVALID_FILE, version = v1)
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(MapFileState.Failed(MapFailure.INVALID_FILE), repository.state.value)
        assertEquals(RejectedMap(url, v1, clock), store.load().rejected)

        repository.refresh()
        settle()

        assertEquals("the same day, the host is not asked again", 1, fetcher.requests.size)

        clock += day
        fetcher.steps += fetcher.unchanged()
        repository.refresh()
        settle()

        assertEquals(2, fetcher.requests.size)
        assertEquals(v1, fetcher.requests.last().knownVersion)
        assertEquals(clock, store.load().rejected!!.atEpochSec)
        assertEquals(MapFileState.Failed(MapFailure.INVALID_FILE), repository.state.value)
    }

    @Test fun `a cut-off download carries on from its part file`() = runTest {
        setUp()
        fetcher.steps += fetcher.fail(MapFailure.NETWORK, afterStarting = v1)
        fetcher.steps += fetcher.complete(v1)
        val repository = started()

        repository.refresh()
        advanceTimeBy(FIRST_RETRY_MS + 1)

        val (first, second) = fetcher.requests
        assertNull(first.partVersion)
        assertEquals(v1, second.partVersion)
        assertEquals(first.part, second.part)
        assertEquals(MapFileState.Ready(second.target), repository.state.value)
    }

    @Test fun `a download from a URL the server no longer gives is dropped`() = runTest {
        setUp()
        val oldPart = File(dir, "map-p.pmtiles.part").apply { writeText("partial") }
        store.save(MapRecord(pending = PendingDownload("https://old.example.org/a.pmtiles", "map-p.pmtiles", v1)))
        fetcher.steps += fetcher.complete(v2)
        val repository = started()

        repository.refresh()
        settle()

        val request = fetcher.requests.single()
        assertNull(request.partVersion)
        assertNotEquals(oldPart, request.part)
        assertFalse(oldPart.exists())
    }

    // Each try costs the driver whatever the host sends before the app hangs up, and a full phone
    // rarely empties itself, so it waits for the driver to come back to the app.
    @Test fun `too little space is said so, and tried at the next refresh, not on a timer`() = runTest {
        setUp()
        fetcher.steps += fetcher.fail(MapFailure.NO_SPACE)
        fetcher.steps += fetcher.complete(v1)
        val repository = started()

        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(MapFileState.Failed(MapFailure.NO_SPACE), repository.state.value)
        assertEquals(1, fetcher.requests.size)

        repository.refresh()
        settle()

        assertTrue(repository.state.value is MapFileState.Ready)
    }

    // A directory where the record's temporary file goes makes every save fail, as a full phone
    // would. The failure must end the check, not escape the app's scope and crash the app: runTest
    // fails a test whose background coroutine throws.
    @Test fun `a record that cannot be saved fails the check instead of crashing the app`() = runTest {
        setUp()
        val repository = started()
        val blocker = File(dir, "map.json.tmp").apply { mkdir() }
        fetcher.steps += fetcher.complete(v1)

        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(MapFileState.Failed(MapFailure.UNEXPECTED), repository.state.value)
        assertEquals("not retried on a timer", 1, fetcher.requests.size)

        blocker.delete()
        fetcher.steps += fetcher.complete(v1)
        repository.refresh()
        settle()

        assertTrue(repository.state.value is MapFileState.Ready)
    }

    // A read-only directory, because start-up cleaning would remove a blocking file before the save.
    @Test fun `a record that cannot be rewritten at start does not crash the app`() = runTest {
        setUp()
        store.save(MapRecord(current = StoredMap(url, "map-gone.pmtiles", v1, clock)))
        dir.setWritable(false)
        try {
            val repository = started()

            assertEquals(MapFileState.None, repository.state.value)
        } finally {
            dir.setWritable(true)
        }
    }

    private inner class FakeMapServer : MapServer {
        val answers = ArrayDeque<MapServerAnswer>()
        var default: MapServerAnswer = MapServerAnswer.File(url)
        val askedAt = mutableListOf<Long>()
        var now: () -> Long = { 0 }
        var gate: CompletableDeferred<Unit>? = null

        override suspend fun ask(): MapServerAnswer {
            askedAt += now()
            gate?.await()
            return answers.removeFirstOrNull() ?: default
        }
    }

    private class FakeFetcher : MapFileFetcher {
        fun interface Step {
            suspend fun run(request: FetchRequest, onVersion: suspend (FileVersion) -> Unit, onProgress: (Long, Long) -> Unit): FetchResult
        }

        val steps = ArrayDeque<Step>()
        val requests = mutableListOf<FetchRequest>()

        override suspend fun fetch(
            request: FetchRequest,
            onVersion: suspend (FileVersion) -> Unit,
            onProgress: (bytes: Long, total: Long) -> Unit,
        ): FetchResult {
            requests += request
            return checkNotNull(steps.removeFirstOrNull()) { "no fetch expected: $request" }.run(request, onVersion, onProgress)
        }

        fun complete(version: FileVersion, progress: List<Pair<Long, Long>> = emptyList()) = Step { request, onVersion, onProgress ->
            onVersion(version)
            for ((bytes, total) in progress) {
                onProgress(bytes, total)
                delay(1)
            }
            request.target.writeText("map")
            FetchResult.Complete(version, 3)
        }

        fun unchanged() = Step { _, _, _ -> FetchResult.Unchanged }

        fun fail(reason: MapFailure, version: FileVersion? = null, afterStarting: FileVersion? = null) = Step { request, onVersion, _ ->
            if (afterStarting != null) {
                onVersion(afterStarting)
                request.part.writeText("partial")
            }
            FetchResult.Failed(reason, version ?: afterStarting)
        }
    }
}
