package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.Session
import java.io.File
import kotlin.coroutines.CoroutineContext

/**
 * [MapRepository]'s decisions, on virtual time, with the server and the file's host faked and the
 * store writing to a real directory.
 */
class MapRepositoryTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val url = "https://maps.example.org/agency.pmtiles"
    private val serverA = "https://a.example.org"
    private val serverB = "https://b.example.org"
    private val v1 = FileVersion(etag = "\"v1\"")
    private val v2 = FileVersion(etag = "\"v2\"")
    private var clock = 1_790_000_000L
    private val day = CHECK_EVERY_S

    private lateinit var dir: File
    private lateinit var store: MapFileStore
    private val server = FakeMapServer()
    private val fetcher = FakeFetcher()
    private val signedIn = Session("https://a.example.org/", "jwt", 0L, email = "d@example.com")
    private val sessions = FakeSessionStore().apply { state.value = signedIn }

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
        MapRepository(server, store, fetcher, backgroundScope, sessions, now = { clock }).also { settle() }

    private fun TestScope.statesOf(repository: MapRepository): List<MapFileState> {
        val states = mutableListOf<MapFileState>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { repository.state.collect { states += it } }
        return states
    }

    /** A map [server] gave, here since [checkedAt], its file on disk. */
    private suspend fun mapHere(
        name: String = "map-a.pmtiles",
        checkedAt: Long = clock,
        from: String = url,
        server: String = serverA,
    ): File {
        store.save(MapRecord(current = StoredMap(from, name, v1, checkedAt, server)))
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
        assertEquals(MapRecord(current = StoredMap(url, target.name, v1, clock, serverA)), store.load())
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

    // B's "no map" used to save an empty record, so back on A the whole map downloaded again (#473).
    @Test fun `a server with no map keeps the last server's map, shown again without asking when it is signed in`() = runTest {
        setUp()
        server.answers += MapServerAnswer.File(url)
        server.answers += MapServerAnswer.NoMap
        fetcher.steps += fetcher.complete(v1)
        val repository = started()
        repository.refresh()
        settle()
        val map = fetcher.requests.single().target

        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        repository.refresh()
        settle()

        assertEquals("B's no map shows the plain map", MapFileState.None, repository.state.value)
        assertEquals(MapRecord(current = StoredMap(url, map.name, v1, clock, serverA)), store.load())

        sessions.state.value = signedIn.copy(token = "jwt2", issuedAtEpochSec = 900L)
        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals("A not asked while its map is recent", 2, server.askedAt.size)
        assertEquals("not downloaded again", 1, fetcher.requests.size)
        started()
        assertTrue("kept at the next start", map.exists())
    }

    // It counts as this server's, so it goes as this server's own map does.
    @Test fun `no map from the server drops a map saved by an older build`() = runTest {
        setUp()
        val map = mapHere()
        store.save(MapRecord(current = store.load().current!!.copy(server = null)))
        server.answers += MapServerAnswer.NoMap
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(MapRecord(), store.load())
        assertEquals(MapFileState.None, repository.state.value)
        assertTrue("a map on screen may still be reading it", map.exists())
    }

    // The client follows the session a moment behind, and used to ask A for B: A's map was then saved as B's (#473).
    @Test fun `while the app still asks the last server, nothing is saved as the new one's map`() = runTest {
        setUp()
        mapHere()
        fetcher.steps += fetcher.unchanged()
        val repository = started()
        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        server.pointsAt = serverA

        repository.refresh()
        settle()

        assertEquals("asked about B", listOf(serverB), server.askedFor)
        assertTrue("and nobody asked", server.askedAt.isEmpty())
        assertEquals("A's map is not relabelled B's", serverA, store.load().current!!.server)
        assertEquals("no failure shown meanwhile", MapFileState.None, repository.state.value)

        server.pointsAt = null
        server.answers += MapServerAnswer.File("https://b-maps.example.org/b.pmtiles")
        fetcher.steps.clear()
        fetcher.steps += fetcher.complete(v2)
        advanceTimeBy(FIRST_RETRY_MS)
        runCurrent()

        assertEquals("B asked once the client caught up", 1, server.askedAt.size)
        val b = store.load().current!!
        assertEquals("https://b-maps.example.org/b.pmtiles" to serverB, b.url to b.server)
        assertEquals(MapFileState.Ready(File(dir, b.fileName)), repository.state.value)
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
        assertEquals(StoredMap(url, new.name, v2, clock, serverA), store.load().current)
        assertTrue("an open map may be reading it", old.exists())

        started()

        assertFalse(old.exists())
        assertTrue(new.exists())
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

    // Otherwise the last server's map stays for up to a day, checked as current (#461).
    @Test fun `a map from another server is not current, so this one is asked at once, and the old file waits for the next start`() = runTest {
        setUp()
        val old = mapHere(from = "https://b-maps.example.org/b.pmtiles", server = serverB)
        fetcher.steps += fetcher.complete(v2, progress = listOf(50L to 100L))
        val repository = started()
        val states = statesOf(repository)

        repository.refresh()
        settle()

        val new = fetcher.requests.single().target
        assertNull("another file says nothing about this one", fetcher.requests.single().knownVersion)
        assertEquals(
            "off the screen at once, then downloaded as a first copy",
            listOf(MapFileState.Ready(old), MapFileState.None, MapFileState.Downloading(50, 100), MapFileState.Ready(new)),
            states,
        )
        assertEquals(StoredMap(url, new.name, v2, clock, serverA), store.load().current)
        assertTrue("a map on screen may still be reading it", old.exists())

        started()

        assertFalse(old.exists())
    }

    @Test fun `a map from another server stays off the screen while this one can't be reached`() = runTest {
        setUp()
        mapHere(from = "https://b-maps.example.org/b.pmtiles", server = serverB)
        server.default = MapServerAnswer.Unreachable
        val repository = started()

        repository.refresh()
        advanceTimeBy(FIRST_RETRY_MS + 1)

        assertEquals(MapFileState.Failed(MapFailure.NETWORK), repository.state.value)
        assertEquals("retried as with no map here", 2, server.askedAt.size)
    }

    /** Signed in to B from A with A's map here, B's check ending with [onB], then signed in to A again. */
    private suspend fun TestScope.backFromB(onB: MapServerAnswer): MapRepository {
        setUp()
        mapHere()
        val repository = started()
        server.answers += onB
        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        repository.refresh()
        settle()
        assertEquals("B's check left the map off the screen", 1, server.askedAt.size)
        assertFalse(repository.state.value is MapFileState.Ready)

        sessions.state.value = signedIn.copy(token = "jwt2", issuedAtEpochSec = 900L)
        repository.refresh()
        advanceTimeBy(60 * 60_000L)
        return repository
    }

    // B's check ended without a map in A's place, and the map stayed blank until the next start or the day's check.
    @Test fun `back on the last server from one that can't be reached, its map is shown again without asking`() = runTest {
        val repository = backFromB(MapServerAnswer.Unreachable)

        assertEquals(MapFileState.Ready(File(dir, "map-a.pmtiles")), repository.state.value)
        assertEquals("B's retry stopped and A was not asked", 1, server.askedAt.size)
        assertTrue(fetcher.requests.isEmpty())
    }

    @Test fun `back on the last server from one that refused the token, its map is shown again without asking`() = runTest {
        val repository = backFromB(MapServerAnswer.SignedOut)

        assertEquals(MapFileState.Ready(File(dir, "map-a.pmtiles")), repository.state.value)
        assertEquals(1, server.askedAt.size)
    }

    // B's first download left its part file pending, so A is asked: the map is shown again while it is.
    @Test fun `back on the last server during the next one's first download, its map is shown while it can't be reached`() = runTest {
        setUp()
        val map = mapHere()
        val repository = started()
        server.answers += MapServerAnswer.File("https://b-maps.example.org/b.pmtiles")
        server.default = MapServerAnswer.Unreachable
        fetcher.steps += FakeFetcher.Step { request, onVersion, onProgress ->
            onVersion(v1)
            request.part.writeText("partial")
            onProgress(10, 100)
            awaitCancellation()
        }
        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        repository.refresh()
        settle()
        assertEquals(MapFileState.Downloading(10, 100), repository.state.value)

        sessions.state.value = signedIn.copy(token = "jwt2", issuedAtEpochSec = 900L)
        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals("asked once, then left for the next refresh as a map is here", 2, server.askedAt.size)
    }

    @Test fun `the same file from another server is only checked, not downloaded again`() = runTest {
        setUp()
        val map = mapHere(server = serverB)
        fetcher.steps += fetcher.unchanged()
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(v1, fetcher.requests.single().knownVersion)
        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals(StoredMap(url, map.name, v1, clock, serverA), store.load().current)
    }

    // Written as an older build wrote map.json, with no server. Before this, a map was saved only while signed in, and
    // a phone holding another server's map is the bug being fixed, so it stays shown but is asked about at once.
    @Test fun `a map saved by an older build still loads, stays shown, and is asked about at once`() = runTest {
        setUp()
        File(dir, "map.json").writeText(
            """{"current":{"url":"$url","fileName":"map-a.pmtiles","version":{"etag":"\"v1\""},"checkedAtEpochSec":$clock}}""",
        )
        val map = File(dir, "map-a.pmtiles").apply { writeText("map") }
        fetcher.steps += fetcher.unchanged()
        val repository = started()
        val states = statesOf(repository)

        assertEquals(StoredMap(url, map.name, v1, clock), store.load().current)
        repository.refresh()
        settle()

        assertEquals("a conditional check, not a download", v1, fetcher.requests.single().knownVersion)
        assertEquals(listOf(MapFileState.Ready(map)), states)
        assertEquals(serverA, store.load().current!!.server)
    }

    @Test fun `with nobody signed in, nobody is asked`() = runTest {
        setUp()
        sessions.state.value = signedIn.copy(token = null)
        val repository = started()

        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertTrue(server.askedAt.isEmpty())
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

    /** How often the server is asked in the hour after a failed first check, once the session becomes [next]. */
    private fun TestScope.asksOnceSignedInAs(next: Session): Int {
        setUp()
        server.default = MapServerAnswer.Unreachable
        val repository = started()
        repository.refresh()
        settle()
        assertEquals(1, server.askedAt.size)

        sessions.state.value = next
        advanceTimeBy(60 * 60_000L)
        return server.askedAt.size
    }

    // A retry left going would keep asking about a server nobody is signed in to.
    @Test fun `a retry stops once its driver signs out`() = runTest {
        assertEquals(1, asksOnceSignedInAs(signedIn.copy(token = null)))
    }

    @Test fun `a retry stops once another server is signed in`() = runTest {
        assertEquals(1, asksOnceSignedInAs(signedIn.copy(serverUrl = "https://b.example.org/")))
    }

    @Test fun `a retry stops once another driver is signed in`() = runTest {
        assertEquals(1, asksOnceSignedInAs(signedIn.copy(email = "e@example.com")))
    }

    @Test fun `a refreshed token keeps the retry going`() = runTest {
        assertTrue(asksOnceSignedInAs(signedIn.copy(token = "jwt2", issuedAtEpochSec = 900L)) > 1)
    }

    // A refresh used to be skipped while the last sign-in's retry waited, up to 5 min, and that retry then asked.
    @Test fun `a refresh for the next sign-in asks at once, and retries from 5 s again`() = runTest {
        setUp()
        server.default = MapServerAnswer.Unreachable
        val repository = started()
        repository.refresh()
        advanceTimeBy(5 * 60_000L)
        val before = server.askedAt.size

        sessions.state.value = signedIn.copy(serverUrl = "https://b.example.org/")
        val start = testScheduler.currentTime
        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        val asked = server.askedAt.drop(before)
        assertEquals("at once", start, asked.first())
        assertEquals("only the new retries", listOf(5_000L, 10_000L, 20_000L), asked.zipWithNext { a, b -> b - a }.take(3))
    }

    @Test fun `a check under way for the last sign-in is stopped, not finished beside the next one's`() = runTest {
        setUp()
        val repository = started()
        val gate = CompletableDeferred<Unit>()
        server.gate = gate
        server.default = MapServerAnswer.NoMap
        repository.refresh()
        runCurrent()

        sessions.state.value = signedIn.copy(serverUrl = "https://b.example.org/")
        repository.refresh()
        runCurrent()
        gate.complete(Unit)
        settle()

        assertEquals("both asked", 2, server.askedAt.size)
        assertEquals("only the new sign-in's check went on", 1, server.answered)
    }

    // The new sign-in's refresh used to be skipped as already under way, and wait out the old backoff (#461).
    @Test fun `signing out and back in to the same server while a retry waits asks at once, and retries from 5 s`() = runTest {
        setUp()
        server.default = MapServerAnswer.Unreachable
        val repository = started()
        repository.refresh()
        advanceTimeBy(5 * 60_000L)
        val before = server.askedAt.size

        sessions.state.value = signedIn.copy(token = null)
        runCurrent()
        sessions.state.value = signedIn.copy(token = "jwt2", issuedAtEpochSec = 900L)
        val start = testScheduler.currentTime
        repository.refresh()
        advanceTimeBy(60_000L)

        val asked = server.askedAt.drop(before)
        assertEquals("at once", start, asked.first())
        assertEquals(listOf(5_000L, 10_000L), asked.zipWithNext { a, b -> b - a }.take(2))
    }

    // Left at Downloading, the progress note stayed until a later check, never if that one ended signed out (#461).
    @Test fun `a first download stopped for the next sign-in doesn't leave its progress showing`() = runTest {
        setUp()
        server.answers += MapServerAnswer.File(url)
        server.answers += MapServerAnswer.SignedOut
        fetcher.steps += FakeFetcher.Step { _, _, onProgress ->
            onProgress(10, 100)
            awaitCancellation()
        }
        val repository = started()
        repository.refresh()
        settle()
        assertEquals(MapFileState.Downloading(10, 100), repository.state.value)

        sessions.state.value = signedIn.copy(serverUrl = "https://b.example.org/")
        repository.refresh()
        settle()

        assertEquals(2, server.askedAt.size)
        assertEquals(MapFileState.None, repository.state.value)
    }

    // The next sign-in's refresh lands as the download finishes: the check is stopped while the record is saved.
    @Test fun `a first download stopped as it is saved doesn't leave its progress showing`() = runTest {
        setUp()
        server.answers += MapServerAnswer.File(url)
        server.answers += MapServerAnswer.SignedOut
        lateinit var repository: MapRepository
        fetcher.steps += FakeFetcher.Step { request, _, onProgress ->
            onProgress(100, 100)
            request.target.writeText("map")
            sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
            repository.refresh()
            FetchResult.Complete(v1, 3)
        }
        repository = started()
        repository.refresh()
        settle()

        assertEquals(2, server.askedAt.size)
        assertEquals(MapFileState.None, repository.state.value)
    }

    // A refresh stopped while it waits for the last one to stop no longer waits for it, so the one after can start
    // while the first is still stopping.
    @Test fun `the next check waits for the last one to stop, even past a refresh stopped while it waited`() = runTest {
        setUp()
        fetcher.steps += FakeFetcher.Step { _, _, onProgress ->
            onProgress(10, 100)
            try {
                awaitCancellation()
            } finally {
                withContext(NonCancellable) { delay(1_000) }
            }
        }
        fetcher.steps += FakeFetcher.Step { _, _, onProgress ->
            onProgress(20, 100)
            awaitCancellation()
        }
        val repository = started()
        repository.refresh()
        settle()

        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        repository.refresh()
        runCurrent()
        sessions.state.value = signedIn.copy(email = "e@example.com")
        repository.refresh()
        runCurrent()
        assertEquals("not while the first still stops", 1, fetcher.requests.size)
        settle()

        assertEquals(2, fetcher.requests.size)
        assertEquals(MapFileState.Downloading(20, 100), repository.state.value)
    }

    @Test fun `a check after a delete still saving waits for it, even past a refresh stopped while it waited`() = runTest {
        setUp()
        val io = HeldDispatcher(StandardTestDispatcher(testScheduler))
        store = MapFileStore(dir, io)
        mapHere(checkedAt = clock - day)
        val repository = started()

        io.holding = true
        repository.delete()
        runCurrent()
        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        repository.refresh()
        runCurrent()
        sessions.state.value = signedIn
        repository.refresh()
        runCurrent()
        io.release()
        settle()

        assertEquals(MapFileState.Deleted, repository.state.value)
        assertTrue("nothing fetched", fetcher.requests.isEmpty())
        assertEquals(MapRecord(deleted = setOf(serverA)), store.load())
    }

    @Test fun `a download that comes keeps the deleted marks as they are then, not as its check began`() = runTest {
        setUp()
        fetcher.steps += FakeFetcher.Step { request, onVersion, _ ->
            onVersion(v1)
            store.save(store.load().copy(deleted = setOf(serverB)))
            request.target.writeText("map")
            FetchResult.Complete(v1, 3)
        }
        val repository = started()

        repository.refresh()
        settle()

        assertTrue(repository.state.value is MapFileState.Ready)
        assertEquals(setOf(serverB), store.load().deleted)
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

    // The phone refuses the URL the same way every time, so a timer would only ask the server
    // again every few minutes for the whole shift.
    @Test fun `plain http the phone refuses is said so, and tried at the next refresh, not on a timer`() = runTest {
        setUp()
        server.answers += MapServerAnswer.File("http://maps.example.org/agency.pmtiles")
        fetcher.steps += fetcher.fail(MapFailure.CLEARTEXT_BLOCKED)
        fetcher.steps += fetcher.complete(v1)
        val repository = started()

        repository.refresh()
        advanceTimeBy(60 * 60_000L)

        assertEquals(MapFileState.Failed(MapFailure.CLEARTEXT_BLOCKED), repository.state.value)
        assertEquals("not retried on a timer", 1, server.askedAt.size)

        // The agency moves the file to https.
        repository.refresh()
        settle()

        assertEquals(url, fetcher.requests.last().url)
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

    /** Puts a directory where `map.json` is, which fails to read as a file. [restore] puts the record back. */
    private fun unreadableRecord(): () -> Unit {
        val record = File(dir, "map.json")
        val aside = File(dir.parentFile, "map-aside.json")
        assertTrue(record.renameTo(aside))
        assertTrue(record.mkdir())
        return {
            assertTrue(record.delete())
            assertTrue(aside.renameTo(record))
        }
    }

    @Test fun `a record that can't be read at start deletes no file`() = runTest {
        setUp()
        val map = mapHere()
        val restore = unreadableRecord()

        val repository = started()

        assertEquals(MapFileState.None, repository.state.value)
        assertTrue(map.exists())
        restore()
        assertEquals(map.name, store.load().current?.fileName)
    }

    @Test fun `a record that can't be read gets nothing, and once it can, a deleted map is still deleted`() = runTest {
        setUp()
        val (repository, _) = readyRepository()
        repository.delete()
        settle()
        val restore = unreadableRecord()

        clock += day
        repository.refresh()
        settle()

        assertEquals(MapFileState.Failed(MapFailure.UNEXPECTED), repository.state.value)
        assertTrue("nothing fetched", fetcher.requests.isEmpty())

        restore()
        repository.refresh()
        settle()

        assertEquals(MapFileState.Deleted, repository.state.value)
        assertEquals(setOf(serverA), store.load().deleted)
        assertTrue(fetcher.requests.isEmpty())
    }

    // --- Deleted by the driver, and got again ---

    /** A map that is here and current, as when the driver opens Offline map. */
    private suspend fun TestScope.readyRepository(): Pair<MapRepository, File> {
        val map = mapHere()
        val repository = started()
        repository.refresh()
        settle()
        assertEquals(MapFileState.Ready(map), repository.state.value)
        return repository to map
    }

    @Test fun `deleting takes the map off the phone at once when no map is open`() = runTest {
        setUp()
        val (repository, map) = readyRepository()

        repository.delete()
        settle()

        assertEquals(MapFileState.Deleted, repository.state.value)
        assertEquals(MapRecord(deleted = setOf(serverA)), store.load())
        assertFalse(map.exists())
    }

    @Test fun `a delete that can't be saved leaves the map, says so, and can be tried again`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        val before = store.load()
        val blocker = File(dir, "map.json.tmp").apply { mkdir() }

        repository.delete()
        settle()

        assertEquals(MapFileState.Ready(map, deleteFailed = true), repository.state.value)
        assertTrue(map.exists())
        assertEquals(before, store.load())

        blocker.delete()
        repository.delete()
        settle()

        assertEquals(MapFileState.Deleted, repository.state.value)
        assertFalse(map.exists())
    }

    @Test fun `with a map open, its file stays until the next start`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        OpenMaps.opened()
        try {
            repository.delete()
            settle()
        } finally {
            OpenMaps.closed()
        }

        assertEquals(MapFileState.Deleted, repository.state.value)
        assertTrue("a map may be reading it", map.exists())

        started()

        assertFalse(map.exists())
    }

    @Test fun `a deleted map stays deleted, and no refresh asks for it, not after a day nor a new start`() = runTest {
        setUp()
        val (repository, _) = readyRepository()
        val asked = server.askedAt.size
        repository.delete()
        settle()

        clock += 2 * day
        repository.refresh()
        settle()
        val next = started()
        assertEquals("nothing on the phone to show", MapFileState.None, next.state.value)
        next.refresh()
        settle()

        assertEquals(MapFileState.Deleted, next.state.value)
        assertEquals("nobody asked", asked, server.askedAt.size)
        assertTrue(fetcher.requests.isEmpty())
    }

    @Test fun `deleting stops a download under way, which then can't bring the map back`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        fetcher.steps += FakeFetcher.Step { request, onVersion, onProgress ->
            onVersion(v2)
            request.part.writeText("partial")
            onProgress(10, 100)
            awaitCancellation()
        }
        val repository = started()
        repository.refresh()
        settle()
        assertEquals(MapFileState.Ready(map, update = MapFileState.Downloading(10, 100)), repository.state.value)

        repository.delete()
        settle()
        repository.refresh()
        settle()

        assertEquals(MapFileState.Deleted, repository.state.value)
        assertEquals(MapRecord(deleted = setOf(serverA)), store.load())
        assertEquals("the part file goes too", setOf("map.json"), dir.list()!!.toSet())
    }

    @Test fun `deleting is per server, so another server's map still comes, and this one stays deleted`() = runTest {
        setUp()
        val (repository, _) = readyRepository()
        repository.delete()
        settle()

        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        fetcher.steps += fetcher.complete(v1)
        repository.refresh()
        settle()
        assertTrue(repository.state.value is MapFileState.Ready)
        assertEquals("the mark is kept through the next server's map", setOf(serverA), store.load().deleted)

        sessions.state.value = signedIn
        repository.refresh()
        settle()
        assertEquals(MapFileState.Deleted, repository.state.value)
        assertEquals(serverB, store.load().current!!.server)
    }

    @Test fun `deleting this server's map leaves another server's`() = runTest {
        setUp()
        val other = mapHere(from = "https://b-maps.example.org/b.pmtiles", server = serverB)
        val repository = started()

        repository.delete()
        settle()

        assertEquals(serverB, store.load().current!!.server)
        assertEquals(setOf(serverA), store.load().deleted)
        assertTrue(other.exists())
    }

    @Test fun `no map from the server keeps the servers whose map was deleted`() = runTest {
        setUp()
        server.default = MapServerAnswer.NoMap
        mapHere()
        store.save(store.load().copy(deleted = setOf(serverB)))
        val repository = started()

        clock += day
        repository.refresh()
        settle()

        assertEquals(MapRecord(deleted = setOf(serverB)), store.load())
    }

    @Test fun `getting a deleted map clears the mark and downloads it as a first copy`() = runTest {
        setUp()
        val (repository, _) = readyRepository()
        repository.delete()
        settle()
        val states = statesOf(repository)
        fetcher.steps += fetcher.complete(v1, progress = listOf(50L to 100L))

        repository.getAgain()
        settle()

        val target = fetcher.requests.single().target
        assertEquals(
            listOf(MapFileState.Deleted, MapFileState.Downloading(0, -1), MapFileState.Downloading(50, 100), MapFileState.Ready(target)),
            states,
        )
        assertEquals(StoredMap(url, target.name, v1, clock, serverA), store.load().current)
        assertEquals(emptySet<String>(), store.load().deleted)
    }

    @Test fun `getting it again downloads a new copy under a new name, even when the one here is current`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        val states = statesOf(repository)
        fetcher.steps += fetcher.complete(v1, progress = listOf(40L to 100L))

        repository.getAgain()
        settle()

        val request = fetcher.requests.single()
        assertNull("whatever version it is", request.knownVersion)
        assertNotEquals(map, request.target)
        assertEquals(
            "the map here stays on screen until the new one is here",
            listOf(
                MapFileState.Ready(map),
                MapFileState.Ready(map, update = MapFileState.Downloading(0, -1)),
                MapFileState.Ready(map, update = MapFileState.Downloading(40, 100)),
                MapFileState.Ready(request.target),
            ),
            states,
        )
        assertTrue("an open map may be reading it", map.exists())
    }

    @Test fun `a new copy asked for is tried again after a broken connection, with the map here kept`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        fetcher.steps += fetcher.fail(MapFailure.NETWORK)
        fetcher.steps += fetcher.complete(v1)

        repository.getAgain()
        settle()
        assertEquals("says it waits to try again", MapFileState.Ready(map, againFailed = MapFileState.Failed(MapFailure.NETWORK)), repository.state.value)

        advanceTimeBy(FIRST_RETRY_MS)
        settle()

        assertEquals(2, fetcher.requests.size)
        assertEquals(MapFileState.Ready(fetcher.requests.last().target), repository.state.value)
    }

    @Test fun `signed out while a new copy waits to be tried again, the page stops saying it tries, also once back`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        fetcher.steps += fetcher.fail(MapFailure.NETWORK)
        repository.getAgain()
        settle()
        assertEquals(MapFileState.Ready(map, againFailed = MapFileState.Failed(MapFailure.NETWORK)), repository.state.value)

        sessions.state.value = signedIn.copy(token = null)
        advanceTimeBy(FIRST_RETRY_MS)
        runCurrent()
        assertEquals(MapFileState.Ready(map), repository.state.value)

        sessions.state.value = signedIn
        repository.refresh()
        settle()
        assertEquals("checked within the day, so nobody is asked", MapFileState.Ready(map), repository.state.value)
        assertEquals(1, fetcher.requests.size)
    }

    @Test fun `another driver signing in while a new copy waits to be tried again stops the page saying it tries`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        fetcher.steps += fetcher.fail(MapFailure.NETWORK)
        repository.getAgain()
        settle()

        sessions.state.value = signedIn.copy(email = "e@example.com")
        repository.refresh()
        settle()

        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals(1, fetcher.requests.size)
    }

    // A new copy downloads beside the map here, so it needs room for both.
    @Test fun `a new copy asked for with no room for it says so beside the map here, with what it needs`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        fetcher.steps += FakeFetcher.Step { _, _, _ -> FetchResult.Failed(MapFailure.NO_SPACE, v1, size = 48_000_000) }

        repository.getAgain()
        settle()

        assertEquals(MapFileState.Ready(map, againFailed = MapFileState.Failed(MapFailure.NO_SPACE, 48_000_000)), repository.state.value)
        advanceTimeBy(FIRST_RETRY_MS)
        runCurrent()
        assertEquals("not tried again on a timer", 1, fetcher.requests.size)
    }

    /** Asks for a new copy of the map here, which fails for [reason]. */
    private suspend fun TestScope.getAgainFailing(reason: MapFailure): Pair<MapRepository, File> {
        setUp()
        val ready = readyRepository()
        fetcher.steps += fetcher.fail(reason)
        ready.first.getAgain()
        settle()
        return ready
    }

    @Test fun `a new copy asked for that is not a map says so beside the map here`() = runTest {
        val (repository, map) = getAgainFailing(MapFailure.INVALID_FILE)
        assertEquals(MapFileState.Ready(map, againFailed = MapFileState.Failed(MapFailure.INVALID_FILE)), repository.state.value)
    }

    @Test fun `a new copy asked for over plain http says so beside the map here`() = runTest {
        val (repository, map) = getAgainFailing(MapFailure.CLEARTEXT_BLOCKED)
        assertEquals(MapFileState.Ready(map, againFailed = MapFileState.Failed(MapFailure.CLEARTEXT_BLOCKED)), repository.state.value)
    }

    @Test fun `a new copy asked for that fails on the phone says so beside the map here`() = runTest {
        val (repository, map) = getAgainFailing(MapFailure.UNEXPECTED)
        assertEquals(MapFileState.Ready(map, againFailed = MapFileState.Failed(MapFailure.UNEXPECTED)), repository.state.value)
    }

    @Test fun `asked for again, a file turned down today is fetched and checked again`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        fetcher.steps += fetcher.fail(MapFailure.INVALID_FILE, version = v2)
        val repository = started()
        repository.refresh()
        settle()
        assertEquals(MapFileState.Ready(map), repository.state.value)
        assertEquals(RejectedMap(url, v2, clock), store.load().rejected)
        fetcher.steps += fetcher.complete(v2)

        repository.getAgain()
        settle()

        assertEquals("the agency may have put it right", 2, fetcher.requests.size)
        assertNull(fetcher.requests.last().knownVersion)
        assertEquals(MapFileState.Ready(fetcher.requests.last().target), repository.state.value)
    }

    @Test fun `a check the app makes by itself stays quiet when it fails`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        fetcher.steps += FakeFetcher.Step { _, _, onProgress ->
            onProgress(10, 100)
            FetchResult.Failed(MapFailure.NO_SPACE, v2, size = 48_000_000)
        }
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(MapFileState.Ready(map), repository.state.value)
    }

    @Test fun `asking again clears what the last try said`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        fetcher.steps += fetcher.fail(MapFailure.UNEXPECTED)
        repository.getAgain()
        settle()
        fetcher.steps += fetcher.complete(v1)

        repository.getAgain()
        settle()

        assertEquals(MapFileState.Ready(fetcher.requests.last().target), repository.state.value)
        assertNotEquals(map, fetcher.requests.last().target)
    }

    @Test fun `a new copy asked for and answered signed out leaves no progress showing`() = runTest {
        setUp()
        val (repository, map) = readyRepository()
        server.answers += MapServerAnswer.SignedOut

        repository.getAgain()
        settle()

        assertEquals(MapFileState.Ready(map), repository.state.value)
    }

    @Test fun `an update keeps the map on screen and shows its progress beside it`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - day)
        fetcher.steps += fetcher.complete(v2, progress = listOf(10L to 100L, 90L to 100L))
        val repository = started()
        val states = statesOf(repository)

        repository.refresh()
        settle()

        assertEquals(
            listOf(
                MapFileState.Ready(map),
                MapFileState.Ready(map, update = MapFileState.Downloading(10, 100)),
                MapFileState.Ready(map, update = MapFileState.Downloading(90, 100)),
                MapFileState.Ready(fetcher.requests.single().target),
            ),
            states,
        )
    }

    @Test fun `too little space says how much the map needs`() = runTest {
        setUp()
        fetcher.steps += FakeFetcher.Step { _, _, _ -> FetchResult.Failed(MapFailure.NO_SPACE, v1, size = 48_000_000) }
        val repository = started()

        repository.refresh()
        settle()

        assertEquals(MapFileState.Failed(MapFailure.NO_SPACE, size = 48_000_000), repository.state.value)
    }

    @Test fun `the details are this server's map's`() = runTest {
        setUp()
        val map = mapHere(checkedAt = clock - 60)
        map.setLastModified(1_789_000_000_000L)
        val repository = started()

        assertEquals(MapDetails(3, 1_789_000_000_000L, clock - 60), repository.details())

        sessions.state.value = signedIn.copy(serverUrl = "$serverB/")
        assertNull("not another server's", repository.details())
        sessions.state.value = signedIn.copy(token = null)
        assertNull("nor with nobody signed in", repository.details())
    }

    /** Hands on what it is given only while not [holding], so a test can catch the store half way through. */
    private class HeldDispatcher(private val next: CoroutineDispatcher) : CoroutineDispatcher() {
        var holding = false
        private val held = ArrayDeque<Pair<CoroutineContext, Runnable>>()

        override fun dispatch(context: CoroutineContext, block: Runnable) {
            if (holding) held += context to block else next.dispatch(context, block)
        }

        fun release() {
            holding = false
            while (held.isNotEmpty()) held.removeFirst().let { (context, block) -> next.dispatch(context, block) }
        }
    }

    private inner class FakeMapServer : MapServer {
        val answers = ArrayDeque<MapServerAnswer>()
        var default: MapServerAnswer = MapServerAnswer.File(url)
        val askedAt = mutableListOf<Long>()
        var answered = 0
        var now: () -> Long = { 0 }
        var gate: CompletableDeferred<Unit>? = null

        /** The servers it was asked about. */
        val askedFor = mutableListOf<String>()

        /** The server the app's API client is still set up for, when it isn't the one asked about. */
        var pointsAt: String? = null

        override suspend fun ask(server: String): MapServerAnswer {
            askedFor += server
            if (pointsAt != null && pointsAt != server) return MapServerAnswer.OtherServer
            askedAt += now()
            gate?.await()
            answered++
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
