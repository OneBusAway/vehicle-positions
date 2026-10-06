package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider

/** [TrackerMapServer] reading `GET /api/v1/map` through the app's real API client. */
class TrackerMapServerTest {
    private lateinit var server: MockWebServer
    private lateinit var mapServer: TrackerMapServer

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        mapServer = TrackerMapServer(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) })
    }

    @After fun tearDown() = server.shutdown()

    @Test fun `a URL is the agency's map file`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":"https://maps.example.org/agency.pmtiles"}"""))

        assertEquals(MapServerAnswer.File("https://maps.example.org/agency.pmtiles"), mapServer.ask())

        val request = server.takeRequest()
        assertEquals("GET", request.method)
        assertEquals("/api/v1/map", request.path)
        assertEquals("Bearer jwt", request.getHeader("Authorization"))
    }

    @Test fun `null means the agency has no map`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":null}"""))

        assertEquals(MapServerAnswer.NoMap, mapServer.ask())
    }

    @Test fun `a blank URL means no map too`() = runTest {
        server.enqueue(MockResponse().setBody("""{"pmtiles_url":"  "}"""))

        assertEquals(MapServerAnswer.NoMap, mapServer.ask())
    }

    // A server from before GET /api/v1/map existed.
    @Test fun `404 means no map`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("404 page not found"))

        assertEquals(MapServerAnswer.NoMap, mapServer.ask())
    }

    @Test fun `401 means the driver must sign in again`() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid token"}"""))

        assertEquals(MapServerAnswer.SignedOut, mapServer.ask())
    }

    @Test fun `a server error is no answer`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"internal server error"}"""))

        assertEquals(MapServerAnswer.Unreachable, mapServer.ask())
    }

    @Test fun `a reply that is not the expected JSON is no answer`() = runTest {
        server.enqueue(MockResponse().setBody("<html>captive portal</html>"))

        assertEquals(MapServerAnswer.Unreachable, mapServer.ask())
    }

    @Test fun `no network is no answer`() = runTest {
        server.shutdown()

        assertEquals(MapServerAnswer.Unreachable, mapServer.ask())
    }

    @Test fun `no server URL yet is no answer, not a crash`() = runTest {
        val unconfigured = TrackerMapServer { error("not signed in") }

        assertEquals(MapServerAnswer.Unreachable, unconfigured.ask())
    }
}
