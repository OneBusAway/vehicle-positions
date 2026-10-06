package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.util.concurrent.TimeUnit

/** [MapFileDownloader] against a local HTTP server, writing to a real directory. */
class MapFileDownloaderTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private lateinit var server: MockWebServer
    private lateinit var dir: File
    private val file = pmtiles(size = 200_000)
    private val versions = mutableListOf<FileVersion>()
    private val progress = mutableListOf<Pair<Long, Long>>()

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        dir = tempFolder.newFolder("map")
    }

    @After fun tearDown() = server.shutdown()

    private val part get() = File(dir, "map-1.pmtiles.part")
    private val target get() = File(dir, "map-1.pmtiles")
    private val url get() = server.url("/maps/agency.pmtiles").toString()

    private fun downloader(freeSpace: Long = Long.MAX_VALUE) = MapFileDownloader(MapFileDownloader.client(), freeSpace = { freeSpace })

    private suspend fun fetch(
        partVersion: FileVersion? = null,
        knownVersion: FileVersion? = null,
        downloader: MapFileDownloader = downloader(),
    ): FetchResult = downloader.fetch(
        FetchRequest(url, part, target, partVersion, knownVersion),
        onVersion = { versions += it },
        onProgress = { bytes, total -> progress += bytes to total },
    )

    private fun body(bytes: ByteArray) = Buffer().write(bytes)

    private fun whole(etag: String = "\"v1\"") = MockResponse().setHeader("ETag", etag).setBody(body(file))

    @Test fun `downloads the whole file and moves it into place`() = runTest {
        server.enqueue(whole())

        val result = fetch()

        assertEquals(FetchResult.Complete(FileVersion(etag = "\"v1\""), file.size.toLong()), result)
        assertArrayEquals(file, target.readBytes())
        assertFalse("the part file is renamed, not left behind", part.exists())
        assertEquals(listOf(FileVersion(etag = "\"v1\"")), versions)
        assertEquals(file.size.toLong() to file.size.toLong(), progress.last())
    }

    @Test fun `asks for the bytes as stored, never gzipped`() = runTest {
        server.enqueue(whole())

        fetch()

        assertEquals("identity", server.takeRequest().getHeader("Accept-Encoding"))
    }

    // The client is the production one: the driver's bearer token belongs to the agency's own
    // server, and the map file is on someone else's host.
    @Test fun `never sends an Authorization header, even after a redirect`() = runTest {
        server.enqueue(MockResponse().setResponseCode(302).setHeader("Location", server.url("/cdn/agency.pmtiles")))
        server.enqueue(whole())

        val result = fetch()

        assertTrue("expected Complete, was $result", result is FetchResult.Complete)
        val first = server.takeRequest()
        val second = server.takeRequest()
        assertEquals("/cdn/agency.pmtiles", second.path)
        assertNull(first.getHeader("Authorization"))
        assertNull(second.getHeader("Authorization"))
    }

    @Test fun `carries on from a part file with Range and If-Range`() = runTest {
        part.writeBytes(file.copyOfRange(0, 50_000))
        server.enqueue(
            MockResponse().setResponseCode(206)
                .setHeader("ETag", "\"v1\"")
                .setHeader("Content-Range", "bytes 50000-${file.size - 1}/${file.size}")
                .setBody(body(file.copyOfRange(50_000, file.size))),
        )

        val result = fetch(partVersion = FileVersion(etag = "\"v1\""))

        assertEquals(FetchResult.Complete(FileVersion(etag = "\"v1\""), file.size.toLong()), result)
        assertArrayEquals(file, target.readBytes())
        val request = server.takeRequest()
        assertEquals("bytes=50000-", request.getHeader("Range"))
        assertEquals("\"v1\"", request.getHeader("If-Range"))
        val (firstBytes, total) = progress.first()
        assertTrue("progress counts the bytes already there, began at $firstBytes", firstBytes > 50_000)
        assertEquals(file.size.toLong(), total)
    }

    @Test fun `resumes on Last-Modified when the host sends no ETag`() = runTest {
        part.writeBytes(file.copyOfRange(0, 50_000))
        val stamp = "Mon, 05 Oct 2026 10:00:00 GMT"
        server.enqueue(
            MockResponse().setResponseCode(206)
                .setHeader("Last-Modified", stamp)
                .setHeader("Content-Range", "bytes 50000-${file.size - 1}/${file.size}")
                .setBody(body(file.copyOfRange(50_000, file.size))),
        )

        fetch(partVersion = FileVersion(lastModified = stamp))

        assertEquals(stamp, server.takeRequest().getHeader("If-Range"))
        assertArrayEquals(file, target.readBytes())
    }

    // What a host does when the file changed since the part began (If-Range no longer matches),
    // or when it does not do ranges at all: it sends the whole file. Splicing that onto the old
    // part would make a corrupt map.
    @Test fun `starts over when the host sends the whole file instead of the rest`() = runTest {
        part.writeBytes(ByteArray(50_000) { 7 })
        server.enqueue(whole(etag = "\"v2\""))

        val result = fetch(partVersion = FileVersion(etag = "\"v1\""))

        assertEquals(FetchResult.Complete(FileVersion(etag = "\"v2\""), file.size.toLong()), result)
        assertArrayEquals(file, target.readBytes())
    }

    @Test fun `starts over once when the host refuses the range`() = runTest {
        part.writeBytes(file.copyOfRange(0, 50_000))
        server.enqueue(MockResponse().setResponseCode(416))
        server.enqueue(whole())

        val result = fetch(partVersion = FileVersion(etag = "\"v1\""))

        assertTrue("expected Complete, was $result", result is FetchResult.Complete)
        assertArrayEquals(file, target.readBytes())
        assertEquals("bytes=50000-", server.takeRequest().getHeader("Range"))
        assertNull("the second request asks for the whole file", server.takeRequest().getHeader("Range"))
    }

    @Test fun `a part from an unknown version is not resumed`() = runTest {
        part.writeBytes(ByteArray(50_000) { 7 })
        server.enqueue(whole())

        fetch(partVersion = FileVersion())

        assertNull(server.takeRequest().getHeader("Range"))
        assertArrayEquals(file, target.readBytes())
    }

    @Test fun `a cut connection keeps the part, and the next try carries on from it`() = runTest {
        server.enqueue(whole().setSocketPolicy(SocketPolicy.DISCONNECT_DURING_RESPONSE_BODY))

        val cut = fetch()

        assertEquals(FetchResult.Failed(MapFailure.NETWORK), cut)
        assertFalse(target.exists())
        val kept = part.length()
        assertTrue("expected part of the file, had $kept bytes", kept in (HEADER_BYTES.toLong() + 1) until file.size.toLong())

        server.enqueue(
            MockResponse().setResponseCode(206)
                .setHeader("ETag", "\"v1\"")
                .setHeader("Content-Range", "bytes $kept-${file.size - 1}/${file.size}")
                .setBody(body(file.copyOfRange(kept.toInt(), file.size))),
        )
        val resumed = fetch(partVersion = versions.single())

        assertTrue("expected Complete, was $resumed", resumed is FetchResult.Complete)
        assertArrayEquals(file, target.readBytes())
    }

    @Test fun `cancelling leaves a part to carry on from`() = runTest {
        server.enqueue(whole().throttleBody(16_384, 50, TimeUnit.MILLISECONDS))
        val someArrived = CompletableDeferred<Unit>()
        val downloader = downloader()

        withContext(Dispatchers.Default) {
            val job = launch {
                downloader.fetch(
                    FetchRequest(url, part, target, null, null),
                    onVersion = {},
                    onProgress = { bytes, _ -> if (bytes >= 32_768) someArrived.complete(Unit) },
                )
            }
            withTimeout(10_000) { someArrived.await() }
            job.cancel()
            job.join()
        }

        assertFalse(target.exists())
        assertTrue("expected the bytes so far, had ${part.length()}", part.length() >= 32_768)
    }

    @Test fun `stops before writing anything when the phone has too little space`() = runTest {
        server.enqueue(whole())

        val result = fetch(downloader = downloader(freeSpace = file.size + SPACE_TO_SPARE - 1))

        assertEquals(FetchResult.Failed(MapFailure.NO_SPACE, FileVersion(etag = "\"v1\"")), result)
        assertFalse(part.exists())
        assertTrue(versions.isEmpty())
    }

    @Test fun `room for the rest of a resumed file is enough`() = runTest {
        part.writeBytes(file.copyOfRange(0, 150_000))
        server.enqueue(
            MockResponse().setResponseCode(206)
                .setHeader("ETag", "\"v1\"")
                .setHeader("Content-Range", "bytes 150000-${file.size - 1}/${file.size}")
                .setBody(body(file.copyOfRange(150_000, file.size))),
        )

        val result = fetch(partVersion = FileVersion(etag = "\"v1\""), downloader = downloader(freeSpace = 50_000 + SPACE_TO_SPARE))

        assertTrue("expected Complete, was $result", result is FetchResult.Complete)
    }

    @Test fun `a response that is not a map file is refused after its first bytes`() = runTest {
        server.enqueue(MockResponse().setHeader("ETag", "\"page\"").setBody("<!doctype html><html>Not found</html>"))

        val result = fetch()

        assertEquals(FetchResult.Failed(MapFailure.INVALID_FILE, FileVersion(etag = "\"page\"")), result)
        assertFalse(part.exists())
        assertFalse(target.exists())
        assertTrue("nothing recorded for a file never written", versions.isEmpty())
    }

    @Test fun `a raster map file is refused`() = runTest {
        server.enqueue(MockResponse().setBody(body(pmtiles(size = 4096, tileType = 2))))

        val result = fetch()

        assertTrue("expected INVALID_FILE, was $result", result is FetchResult.Failed && result.reason == MapFailure.INVALID_FILE)
        assertFalse(part.exists())
        assertFalse(target.exists())
    }

    // The host's length and the bytes agree, so only the file's own header can tell it was cut
    // short when it was uploaded.
    @Test fun `a file shorter than its own header says is refused`() = runTest {
        server.enqueue(MockResponse().setBody(body(file.copyOfRange(0, 150_000))))

        val result = fetch()

        assertTrue("expected INVALID_FILE, was $result", result is FetchResult.Failed && result.reason == MapFailure.INVALID_FILE)
        assertFalse(part.exists())
        assertFalse(target.exists())
    }

    @Test fun `the host still has the version here, so nothing is downloaded`() = runTest {
        server.enqueue(MockResponse().setResponseCode(304))

        val result = fetch(knownVersion = FileVersion(etag = "\"v1\""))

        assertEquals(FetchResult.Unchanged, result)
        assertEquals("\"v1\"", server.takeRequest().getHeader("If-None-Match"))
        assertFalse(target.exists())
    }

    @Test fun `asks If-Modified-Since when all it has is a date`() = runTest {
        server.enqueue(MockResponse().setResponseCode(304))
        val stamp = "Mon, 05 Oct 2026 10:00:00 GMT"

        fetch(knownVersion = FileVersion(lastModified = stamp))

        val request = server.takeRequest()
        assertEquals(stamp, request.getHeader("If-Modified-Since"))
        assertNull(request.getHeader("If-None-Match"))
    }

    // Some hosts send 200 whatever the request says. Without this, every phone would download the
    // whole map again at every daily check.
    @Test fun `a host that ignores the condition is caught by its ETag`() = runTest {
        server.enqueue(whole(etag = "\"v1\""))

        val result = fetch(knownVersion = FileVersion(etag = "\"v1\""))

        assertEquals(FetchResult.Unchanged, result)
        assertFalse(part.exists())
        assertFalse(target.exists())
    }

    @Test fun `a newer version is downloaded`() = runTest {
        server.enqueue(whole(etag = "\"v2\""))

        val result = fetch(knownVersion = FileVersion(etag = "\"v1\""))

        assertEquals(FetchResult.Complete(FileVersion(etag = "\"v2\""), file.size.toLong()), result)
    }

    @Test fun `a server error is a network failure`() = runTest {
        server.enqueue(MockResponse().setResponseCode(503))

        assertEquals(FetchResult.Failed(MapFailure.NETWORK, FileVersion()), fetch())
    }

    @Test fun `no server is a network failure`() = runTest {
        server.shutdown()

        assertEquals(FetchResult.Failed(MapFailure.NETWORK), fetch())
    }
}

/** [headerMatchesLength], the check that keeps a truncated file from ever being opened. */
class PmtilesHeaderTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private fun check(bytes: ByteArray) = headerMatchesLength(tempFolder.newFile().apply { writeBytes(bytes) })

    @Test fun `a whole file passes`() = assertTrue(check(pmtiles()))

    @Test fun `a file cut short fails`() = assertFalse(check(pmtiles().copyOfRange(0, 4000)))

    @Test fun `bytes past the last section fail`() = assertFalse(check(pmtiles() + ByteArray(10)))

    @Test fun `a file shorter than the header fails`() = assertFalse(check(pmtiles().copyOfRange(0, HEADER_BYTES - 1)))

    @Test fun `another version fails`() = assertFalse(check(pmtiles().also { it[7] = 2 }))

    @Test fun `another format fails`() = assertFalse(check(pmtiles().also { it[0] = 'X'.code.toByte() }))

    @Test fun `raster tiles fail`() = assertFalse(check(pmtiles(tileType = 2)))

    @Test fun `a root directory inside the header fails`() = assertFalse(
        check(pmtiles().also { java.nio.ByteBuffer.wrap(it).order(java.nio.ByteOrder.LITTLE_ENDIAN).putLong(8, 100) }),
    )

    @Test fun `a negative length fails`() = assertFalse(
        check(pmtiles().also { java.nio.ByteBuffer.wrap(it).order(java.nio.ByteOrder.LITTLE_ENDIAN).putLong(48, -1) }),
    )
}
