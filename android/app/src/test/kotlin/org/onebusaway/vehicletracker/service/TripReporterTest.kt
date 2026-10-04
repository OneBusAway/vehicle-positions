package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider

class TripReporterTest {
    private val trip = ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L)
    private fun fix(ts: Long = 1000L) = LocationFix(-1.29, 36.82, bearing = 180.0, speed = 8.5, accuracy = 12.0, timeEpochSec = ts)

    /** A clock that has moved on a whole report interval at every reading, so no report is held back. */
    private fun steppingClock(): () -> Long {
        var now = 0L
        return { now += REPORT_INTERVAL_MS; now }
    }

    private fun reporterWith(
        server: MockWebServer,
        elapsedMs: () -> Long = steppingClock(),
    ): Pair<TripReporter, TrackingRepository> {
        val tracking = TrackingRepository()
        val api = ApiFactory { "jwt" }.create(server.url("/").toString())
        return TripReporter(TrackerApiProvider { api }, tracking, elapsedMs) to tracking
    }

    @Test fun `successful send increments counter and clears problem`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        val (reporter, tracking) = reporterWith(server)

        reporter.report(trip, fix())

        assertEquals(1, tracking.state.value.fixesSent)
        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        server.shutdown()
    }

    @Test fun `network failure sets NO_NETWORK and success recovers`() = runTest {
        val server = MockWebServer().apply { start() }
        val (reporter, tracking) = reporterWith(server)
        server.shutdown() // force connection failure

        reporter.report(trip, fix())
        assertEquals(TrackingProblem.NO_NETWORK, tracking.state.value.problem)

        val server2 = MockWebServer().apply { start() }
        server2.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        val api2 = ApiFactory { "jwt" }.create(server2.url("/").toString())
        val reporter2 = TripReporter(TrackerApiProvider { api2 }, tracking, steppingClock())
        reporter2.report(trip, fix())
        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        server2.shutdown()
    }

    @Test fun `401 sets AUTH_EXPIRED`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid token"}"""))
        val (reporter, tracking) = reporterWith(server)

        reporter.report(trip, fix())

        assertEquals(TrackingProblem.AUTH_EXPIRED, tracking.state.value.problem)
        server.shutdown()
    }

    @Test fun `three consecutive timestamp 400s set CLOCK_SKEW`() = runTest {
        val server = MockWebServer().apply { start() }
        repeat(3) {
            server.enqueue(MockResponse().setResponseCode(400)
                .setBody("""{"error":"timestamp must be within 5 minutes of server time"}"""))
        }
        val (reporter, tracking) = reporterWith(server)

        repeat(2) { reporter.report(trip, fix()) }
        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        reporter.report(trip, fix())
        assertEquals(TrackingProblem.CLOCK_SKEW, tracking.state.value.problem)
        server.shutdown()
    }

    @Test fun `429 is dropped silently`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(429).setBody("""{"error":"rate limit exceeded"}"""))
        val (reporter, tracking) = reporterWith(server)

        reporter.report(trip, fix())

        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        assertEquals(0, tracking.state.value.fixesSent)
        server.shutdown()
    }

    @Test fun `gps unavailable takes precedence until restored`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        val (reporter, tracking) = reporterWith(server)

        reporter.gpsAvailable(false)
        assertEquals(TrackingProblem.NO_GPS, tracking.state.value.problem)
        reporter.report(trip, fix())
        assertEquals(TrackingProblem.NO_GPS, tracking.state.value.problem)
        reporter.gpsAvailable(true)
        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        server.shutdown()
    }

    @Test fun `invalid bearing dropped and negative speed clamped`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        val (reporter, _) = reporterWith(server)

        reporter.report(trip, LocationFix(-1.29, 36.82, bearing = 361.0, speed = -1.0, accuracy = null, timeEpochSec = 1000L))

        val sent = kotlinx.serialization.json.Json.parseToJsonElement(
            server.takeRequest().body.readUtf8()).jsonObject
        org.junit.Assert.assertFalse(sent.containsKey("bearing"))
        assertEquals("0.0", sent["speed"].toString())
        server.shutdown()
    }

    @Test fun `HTTP 500 leaves status unchanged and does not increment counter`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"internal error"}"""))
        val (reporter, tracking) = reporterWith(server)

        reporter.report(trip, fix())

        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        assertEquals(0, tracking.state.value.fixesSent)
        server.shutdown()
    }

    @Test fun `400 without timestamp in body leaves status unchanged and does not increment counter`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(400).setBody("""{"error":"invalid vehicle_id"}"""))
        val (reporter, tracking) = reporterWith(server)

        reporter.report(trip, fix())

        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        assertEquals(0, tracking.state.value.fixesSent)
        server.shutdown()
    }

    @Test fun `success resets the consecutive timestamp reject streak`() = runTest {
        val server = MockWebServer().apply { start() }
        repeat(2) {
            server.enqueue(MockResponse().setResponseCode(400)
                .setBody("""{"error":"timestamp must be within 5 minutes of server time"}"""))
        }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        repeat(2) {
            server.enqueue(MockResponse().setResponseCode(400)
                .setBody("""{"error":"timestamp must be within 5 minutes of server time"}"""))
        }
        val (reporter, tracking) = reporterWith(server)

        repeat(2) { reporter.report(trip, fix()) } // two rejects, no flip yet
        reporter.report(trip, fix()) // success resets streak
        repeat(2) { reporter.report(trip, fix()) } // two more rejects, streak restarted so still below threshold

        assertEquals(TrackingProblem.NONE, tracking.state.value.problem)
        server.shutdown()
    }

    @Test fun `report sends route_id and start_date, and trip_id only when a gtfs id exists`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        val (reporter, _) = reporterWith(server)

        reporter.report(trip, fix())
        val withTrip = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals("trip-0830", withTrip["trip_id"]!!.jsonPrimitive.content)
        assertEquals("5", withTrip["route_id"]!!.jsonPrimitive.content)
        assertEquals("20260804", withTrip["start_date"]!!.jsonPrimitive.content)

        reporter.report(trip.copy(gtfsTripId = ""), fix())
        val routeOnly = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals(false, routeOnly.containsKey("trip_id"))
        assertEquals("5", routeOnly["route_id"]!!.jsonPrimitive.content)
        server.shutdown()
    }

    @Test fun `fixes a second apart are reported about as often as fixes ten seconds apart`() = runTest {
        val server = MockWebServer().apply { start() }
        repeat(3) { server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}""")) }
        var now = 0L
        val (reporter, tracking) = reporterWith(server) { now }

        repeat(21) { second ->
            now = second * 1_000L
            reporter.report(trip, fix())
        }

        // The fixes at 0 s, 9 s and 18 s: the pace at which fixes asked for ten seconds apart arrive.
        assertEquals(3, server.requestCount)
        assertEquals(3, tracking.state.value.fixesSent)
        server.shutdown()
    }

    /**
     * With no screen open the service asks for a fix every ten seconds, and Android hands each one
     * over about nine seconds after the last. Every one of them is a report: a threshold that held
     * back the early ones would halve the rate.
     */
    @Test fun `fixes asked for ten seconds apart are all reported, as early as they arrive`() = runTest {
        val server = MockWebServer().apply { start() }
        val arrivals = listOf(0L, 9_050L, 18_000L, 27_100L, 37_100L, 46_000L)
        repeat(arrivals.size) { server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}""")) }
        var now = 0L
        val (reporter, tracking) = reporterWith(server) { now }

        for (arrival in arrivals) {
            now = arrival
            reporter.report(trip, fix())
        }

        assertEquals(arrivals.size, tracking.state.value.fixesSent)
        server.shutdown()
    }

    @Test fun `a fix one millisecond short of due is held back`() = runTest {
        val server = MockWebServer().apply { start() }
        repeat(2) { server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}""")) }
        var now = 0L
        val (reporter, _) = reporterWith(server) { now }

        reporter.report(trip, fix())
        now = 8_499L
        reporter.report(trip, fix())
        assertEquals(1, server.requestCount)

        now = 8_500L
        reporter.report(trip, fix())
        assertEquals(2, server.requestCount)
        server.shutdown()
    }

    @Test fun `a fix held back changes nothing the driver sees`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid token"}"""))
        var now = 0L
        val (reporter, tracking) = reporterWith(server) { now }
        reporter.report(trip, fix())
        val afterFirst = tracking.state.value

        now = 1_000L
        reporter.report(trip, fix())

        assertEquals(1, server.requestCount)
        assertEquals(afterFirst, tracking.state.value)
        assertEquals(TrackingProblem.AUTH_EXPIRED, tracking.state.value.problem)
        server.shutdown()
    }
}
