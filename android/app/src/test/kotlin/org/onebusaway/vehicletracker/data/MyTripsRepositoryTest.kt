package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.ApiHolder
import java.time.Instant
import java.util.concurrent.TimeUnit

/** [MyTripsRepository] through the app's own client, against what the server writes. */
class MyTripsRepositoryTest {
    private val server = MockWebServer()
    private val sessions = FakeSessionStore()

    @Before fun setUp() {
        server.start()
        sessions.state.value = Session(server.url("/").toString(), "a1", ISSUED_AT, "r1", ISSUED_AT + 900)
    }

    @After fun tearDown() = server.shutdown()

    private fun repository(): MyTripsRepository {
        val holder = ApiHolder(sessions, CoroutineScope(Job().apply { cancel() }), { NOW })
        return MyTripsRepository(TrackerApiProvider(holder::api))
    }

    private fun logged() = AppLog.recent.snapshot().count { it.tag == "MyTripsRepository" }

    @Test fun `a page is asked for with the bearer token, and the server's own times are read`() = runTest {
        // As driver_trip_handlers.go writes it: Go's time with its microseconds, a null route, no end_time while running.
        server.enqueue(MockResponse().setBody(
            """{"count":2,"has_more":true,"trips":[
              {"id":9,"vehicle_id":"4317","vehicle_label":"Bus 4317","route_id":"","route":null,"gtfs_trip_id":"T9","headsign":"","start_time":"2026-11-28T09:20:29.611579+05:30","status":"active"},
              {"id":8,"vehicle_id":"4401","vehicle_label":"","route_id":"100479","route":{"short_name":"7","long_name":"Rainier Beach","color":"FFC72C","text_color":"000000"},"gtfs_trip_id":"T8","headsign":"Downtown Seattle","start_time":"2026-11-28T03:50:01.123456Z","end_time":"2026-11-28T04:39:00Z","status":"completed"}
            ]}""",
        ))

        val page = repository().page(limit = 50, offset = 100).getOrThrow()

        val sent = server.takeRequest()
        assertEquals("GET", sent.method)
        assertEquals("/api/v1/trips", sent.requestUrl?.encodedPath)
        assertEquals("50", sent.requestUrl?.queryParameter("limit"))
        assertEquals("100", sent.requestUrl?.queryParameter("offset"))
        assertEquals("Bearer a1", sent.getHeader("Authorization"))
        assertTrue(page.hasMore)
        assertEquals(listOf(9L, 8L), page.trips.map { it.id })
        assertNull(page.trips[0].route)
        assertNull(page.trips[0].end)
        assertEquals(Instant.parse("2026-11-28T03:50:29.611579Z"), page.trips[0].start)
        assertEquals(MyTripRoute("7", "Rainier Beach", "FFC72C", "000000"), page.trips[1].route)
        assertEquals(Instant.parse("2026-11-28T04:39:00Z"), page.trips[1].end)
    }

    @Test fun `a sign-in the server ended is told apart`() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid or expired token"}"""))
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid or expired refresh token"}"""))

        assertEquals(ApiError.Unauthorized, repository().page(50, 0).exceptionOrNull())
    }

    // Aaron on #687: leaving the page while it loads is not a failure, so it leaves no warning in Share diagnostics.
    @Test fun `a load the driver leaves is not logged, and one that fails is`() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        val before = logged()
        val load = CoroutineScope(Dispatchers.IO).async { repository().page(50, 0) }
        assertNotNull("the request went out", server.takeRequest(5, TimeUnit.SECONDS))

        load.cancelAndJoin()

        assertEquals(before, logged())

        server.enqueue(MockResponse().setResponseCode(500))
        assertTrue(repository().page(50, 0).isFailure)
        assertEquals(before + 1, logged())
    }

    private companion object {
        const val ISSUED_AT = 1_785_886_200L
        const val NOW = ISSUED_AT + 600
    }
}
