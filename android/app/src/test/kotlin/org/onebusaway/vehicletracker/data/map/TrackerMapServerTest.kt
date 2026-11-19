package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.BoundApi
import org.onebusaway.vehicletracker.data.normalizedServerUrl
import org.onebusaway.vehicletracker.di.ApiHolder

/** [TrackerMapServer] reading `GET /api/v1/map` through the app's real API client. */
class TrackerMapServerTest {
    private lateinit var server: MockWebServer
    private lateinit var serverUrl: String
    private lateinit var mapServer: TrackerMapServer

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        serverUrl = server.url("/").toString()
        mapServer = TrackerMapServer { BoundApi(serverUrl, ApiFactory { "jwt" }.create(serverUrl)) }
    }

    /** Asks about the server the client is set up for, as MapRepository names it. */
    private suspend fun TrackerMapServer.askIt() = ask(normalizedServerUrl(serverUrl))

    @After fun tearDown() = server.shutdown()

    @Test fun `a URL is the agency's map file`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":"https://maps.example.org/agency.pmtiles"}"""))

        assertEquals(MapServerAnswer.File("https://maps.example.org/agency.pmtiles"), mapServer.askIt())

        val request = server.takeRequest()
        assertEquals("GET", request.method)
        assertEquals("/api/v1/map", request.path)
        assertEquals("Bearer jwt", request.getHeader("Authorization"))
    }

    @Test fun `null means the agency has no map`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":null}"""))

        assertEquals(MapServerAnswer.NoMap, mapServer.askIt())
    }

    @Test fun `a blank URL means no map too`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":"  "}"""))

        assertEquals(MapServerAnswer.NoMap, mapServer.askIt())
    }

    // A server from before GET /api/v1/map existed.
    @Test fun `404 means no map`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("404 page not found"))

        assertEquals(MapServerAnswer.NoMap, mapServer.askIt())
    }

    @Test fun `401 means the driver must sign in again`() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid token"}"""))

        assertEquals(MapServerAnswer.SignedOut, mapServer.askIt())
    }

    @Test fun `a server error is no answer`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"internal server error"}"""))

        assertEquals(MapServerAnswer.Unreachable, mapServer.askIt())
    }

    @Test fun `a reply that is not the expected JSON is no answer`() = runTest {
        server.enqueue(MockResponse().setBody("<html>captive portal</html>"))

        assertEquals(MapServerAnswer.Unreachable, mapServer.askIt())
    }

    @Test fun `no network is no answer`() = runTest {
        server.shutdown()

        assertEquals(MapServerAnswer.Unreachable, mapServer.askIt())
    }

    // ApiHolder follows the session through a collector, a moment behind it. A request to A after B's sign-in got A's
    // map saved as B's, and if A refused A's token, it went to A again with B's (#473).
    @Test fun `a client still set up for the last server asks nobody`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":"https://maps.example.org/a.pmtiles"}"""))
        val sessions = FakeSessionStore().apply { state.value = Session(serverUrl, "jwt-a", 0L) }
        val holder = ApiHolder(sessions, CoroutineScope(Job().apply { cancel() }), clock = { 0L })
        holder.api()
        sessions.state.value = Session("https://b.example.org/", "jwt-b", 0L)

        assertEquals(MapServerAnswer.OtherServer, TrackerMapServer(holder::boundApi).ask("https://b.example.org"))
        assertEquals(0, server.requestCount)
        assertEquals(
            "A asked about A",
            MapServerAnswer.File("https://maps.example.org/a.pmtiles"),
            TrackerMapServer(holder::boundApi).askIt(),
        )
    }

    @Test fun `with no server set up, nobody is asked`() = runTest {
        val holder = ApiHolder(FakeSessionStore(), CoroutineScope(Job().apply { cancel() }), clock = { 0L })

        assertEquals(MapServerAnswer.OtherServer, TrackerMapServer(holder::boundApi).askIt())
        assertEquals(0, server.requestCount)
    }

    @Test fun `a server URL the client can't take is no answer, not a crash`() = runTest {
        val unusable = TrackerMapServer { error("not a URL") }

        assertEquals(MapServerAnswer.Unreachable, unusable.askIt())
    }
}
