package org.onebusaway.vehicletracker.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** The app's recent warnings, kept for Share diagnostics while the app runs, and what is scrubbed out of them. */
class RecentProblemsTest {
    private var now = 1_790_000_000_000L
    private val jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiI0MiIsImVtYWlsIjoibWFyaWFAbWV0cm8uZXhhbXBsZSJ9.c2lnbmF0dXJl"
    private val refreshToken = "r1-7f3a9c2e5b8d4f6a1c0e9b7d"

    private fun ring(capacity: Int = RecentProblems.CAPACITY) = RecentProblems(capacity) { now }

    @Test fun `it keeps the newest 50, oldest first, and drops the oldest`() {
        val ring = ring()

        repeat(60) { ring.add("Tag", "problem $it") }

        val kept = ring.snapshot().map { it.message }
        assertEquals((10 until 60).map { "problem $it" }, kept)
    }

    @Test fun `each line has the time, the tag, the words and what was thrown`() {
        val ring = ring()

        ring.add("HistoryUploader", "Keeping queued fixes after network failure", IOException("timeout"))
        now += 1_000
        ring.add("MapRepository", "Map file not downloaded: NETWORK")

        assertEquals(
            listOf(
                RecentProblem(1_790_000_000_000L, "HistoryUploader", "Keeping queued fixes after network failure (IOException: timeout)"),
                RecentProblem(1_790_000_001_000L, "MapRepository", "Map file not downloaded: NETWORK"),
            ),
            ring.snapshot(),
        )
    }

    @Test fun `a line is one line, cut short at the limit`() {
        val ring = ring()

        ring.add("Tag", "first\nsecond", IllegalStateException("x".repeat(1_000)))

        val message = ring.snapshot().single().message
        assertTrue(message, message.startsWith("first second (IllegalStateException: xxx"))
        assertEquals(RecentProblems.LINE_LIMIT, message.length)
        assertTrue(message.endsWith("…"))
    }

    // The service, the uploader and the screens all log, each on its own thread.
    @Test fun `writes from several threads at once all land, and none throws`() {
        val threads = 8
        val each = 500
        val all = ring(capacity = threads * each)
        val newest = ring()
        val pool = Executors.newFixedThreadPool(threads)
        val start = CountDownLatch(1)
        val failures = (0 until threads).map { thread ->
            pool.submit<Unit> {
                start.await()
                repeat(each) {
                    all.add("T$thread", "$thread-$it")
                    newest.add("T$thread", "$thread-$it")
                }
            }
        }
        start.countDown()
        failures.forEach { it.get(30, TimeUnit.SECONDS) }
        pool.shutdown()

        val lines = all.snapshot().map { it.message }
        assertEquals(threads * each, lines.size)
        assertEquals("none twice, none lost", (0 until threads).flatMap { t -> (0 until each).map { "$t-$it" } }.toSet(), lines.toSet())
        for (thread in 0 until threads) {
            val own = lines.filter { it.startsWith("$thread-") }
            assertEquals("each thread's in its own order", (0 until each).map { "$thread-$it" }, own)
        }
        assertEquals(RecentProblems.CAPACITY, newest.snapshot().size)
    }

    // Share diagnostics reads the list while the service may still be logging.
    @Test fun `reading while others write never throws and never sees a half-written list`() {
        val ring = ring()
        val pool = Executors.newFixedThreadPool(5)
        val start = CountDownLatch(1)
        val writers = (0 until 4).map { thread ->
            pool.submit<Unit> {
                start.await()
                repeat(20_000) { ring.add("T$thread", "$thread-$it") }
            }
        }
        val reader = pool.submit<Int> {
            start.await()
            var reads = 0
            while (writers.any { !it.isDone }) {
                val lines = ring.snapshot()
                check(lines.size <= RecentProblems.CAPACITY) { "${lines.size} lines" }
                check(lines.all { it.message.isNotEmpty() }) { "a line missing" }
                reads++
            }
            reads
        }
        start.countDown()
        writers.forEach { it.get(60, TimeUnit.SECONDS) }
        assertTrue("read while writing", reader.get(60, TimeUnit.SECONDS) > 0)
        pool.shutdown()
    }

    @Test fun `a warning through AppLog is kept`() {
        val marker = "a warning ${System.nanoTime()}"

        AppLog.w("RecentProblemsTest", marker, IOException("disk full"))

        assertEquals(
            "RecentProblemsTest" to "$marker (IOException: disk full)",
            AppLog.recent.snapshot().last().let { it.tag to it.message },
        )
    }

    // A reply that fails to parse can be quoted in its exception: a sign-in reply holds both tokens.
    @Test fun `tokens are scrubbed before the line is kept`() {
        val ring = ring()

        ring.add(
            "AuthRepository",
            "Could not read the reply",
            IllegalArgumentException("""Unexpected JSON at offset 9: {"access_token":"$jwt","refresh_token":"$refreshToken"}"""),
        )

        val message = ring.snapshot().single().message
        for (secret in listOf(jwt.take(12), "r1-7f3a", "c2lnbmF0dXJl")) {
            assertFalse("\"$secret\" kept: $message", message.contains(secret))
        }
        assertTrue(message, message.startsWith("Could not read the reply (IllegalArgumentException: Unexpected JSON at offset 9: "))
    }

    // Cut first, the token's first characters would be too few to look like one, and would be sent.
    @Test fun `a token across the cut is scrubbed whole, not left in pieces`() {
        val ring = ring()

        ring.add("Tag", "x ".repeat(142) + refreshToken)

        val message = ring.snapshot().single().message
        assertFalse(message, message.contains("r1-7f3a"))
        assertTrue(message, message.endsWith("<token>"))
    }

    @Test fun `the scrubber removes a token, a refresh token and an Authorization header`() {
        assertEquals("Sent <token>", scrubbed("Sent $jwt"))
        assertEquals("Refresh <token> refused", scrubbed("Refresh $refreshToken refused"))
        assertEquals("Authorization: <hidden>", scrubbed("Authorization: Bearer opaque"))
        assertEquals("authorization=<hidden>, then", scrubbed("authorization=Basic ZHJpdmVy, then"))
        assertEquals("header Bearer <token>", scrubbed("header Bearer opaque.value"))
    }

    @Test fun `the scrubber removes a password, in a URL or a field`() {
        assertEquals(
            "Failed to connect to https://tracker.example.org",
            scrubbed("Failed to connect to https://maria:hunter2@tracker.example.org:8443/api/v1/auth/login?next=/runs"),
        )
        assertEquals("""{"email":"<email>","password":<hidden>}""", scrubbed("""{"email":"maria@metro.example","password":"hunter2"}"""))
        assertEquals("password=<hidden>&token=<hidden>", scrubbed("password=hunter2&token=abc"))
    }

    @Test fun `the scrubber removes an email, a path with ids in it, and a position`() {
        assertEquals("Not <email>", scrubbed("Not maria@metro.example"))
        assertEquals("GET http://10.0.2.2 failed", scrubbed("GET http://10.0.2.2:8080/api/v1/vehicles/4317/trips/88123 failed"))
        assertEquals("At <number>,<number>", scrubbed("At 47.60970,-122.33310"))
    }

    // What helps whoever looks after the app stays: the words, HTTP codes, hosts, addresses and versions.
    @Test fun `the scrubber leaves the rest as it was`() {
        for (text in listOf(
            "Dropping location report after HTTP 503",
            "Unable to resolve host \"tracker.example.org\": No address associated with hostname",
            "Failed to connect to /10.0.2.2:8080",
            "Could not encrypt the stored token; leaving it for the next launch.",
            "Android 16.0.1 refused the tracking service foreground status",
            "Gave up after 2.5 s",
        )) {
            assertEquals(text, scrubbed(text))
        }
    }
}
