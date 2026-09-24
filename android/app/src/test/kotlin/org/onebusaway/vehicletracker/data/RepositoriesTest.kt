package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.ApiHolder
import java.io.IOException
import java.time.ZoneId
import java.time.ZoneOffset

class RepositoriesTest {
    private fun apiFor(server: MockWebServer) =
        ApiFactory { "jwt" }.create(server.url("/").toString())

    @Test fun `login success stores url, token, and issue time`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"token":"jwt-1"}"""))
        val sessions = FakeSessionStore()
        val repo = AuthRepository(sessions, ApiFactory { null }, clock = { 1000L })

        val result = repo.login(server.url("/").toString(), "d@example.com", "pw")

        assertTrue(result.isSuccess)
        val s = sessions.session.first()
        assertEquals("jwt-1", s.token)
        assertEquals(1000L, s.issuedAtEpochSec)
        server.shutdown()
    }

    @Test fun `login 401 maps to Unauthorized`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid email or password"}"""))
        val repo = AuthRepository(FakeSessionStore(), ApiFactory { null }, clock = { 0L })

        val result = repo.login(server.url("/").toString(), "d@example.com", "bad")

        assertTrue(result.exceptionOrNull() is ApiError.Unauthorized)
        server.shutdown()
    }

    @Test fun `hasFreshToken is false after 24h`() {
        val s = Session("u", "t", issuedAtEpochSec = 0L)
        assertTrue(s.hasFreshToken(nowEpochSec = 24 * 3600 - 1))
        assertTrue(!s.hasFreshToken(nowEpochSec = 24 * 3600))
    }

    @Test fun `trip start saves the catalog's service date, not the device's calendar date`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"trip-0830","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val store = FakeTripStateStore()
        // The clock is 2026-08-04T23:30:00Z, which is already the 5th in Nairobi and still the
        // 4th in Los Angeles — the device's date is the wrong answer either way for a run the
        // catalog dated the 3rd, as an after-midnight departure is.
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, store, FakeVehiclePrefsStore(), clock = { 1_785_886_200L })

        val result = repo.start("bus-1", "5", "trip-0830", "20260803")

        assertTrue(result.isSuccess)
        val trip = store.activeTrip.first()!!
        assertEquals(7L, trip.tripDbId)
        assertEquals("trip-0830", trip.gtfsTripId)
        assertEquals("5", trip.routeId)
        assertEquals("20260803", trip.startDate)
        assertEquals(1_785_886_200L, trip.startedAtEpochSec)
        assertEquals(listOf("5"), store.recentRoutes.first())
        server.shutdown()
    }

    @Test fun `trip start keeps gtfs trip id blank instead of copying the route id`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":8,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, store, FakeVehiclePrefsStore(), clock = { 500L })

        repo.start("bus-1", " 5 ", "  ", "20260804")

        val trip = store.activeTrip.first()!!
        assertEquals("", trip.gtfsTripId)
        assertEquals("5", trip.routeId)
        assertEquals("20260804", trip.startDate)
        val sentBody = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals("5", sentBody["route_id"]!!.jsonPrimitive.content)
        assertEquals(listOf("5"), store.recentRoutes.first())
        server.shutdown()
    }

    @Test fun `serviceDate formats the local calendar date as YYYYMMDD`() {
        assertEquals("19700101", serviceDate(0L, ZoneOffset.UTC))
        assertEquals("19691231", serviceDate(0L, ZoneId.of("America/Los_Angeles")))
        assertEquals("20260805", serviceDate(1_785_886_200L, ZoneId.of("Africa/Nairobi")))
    }

    @Test fun `trip start 403 maps to NotAssigned and 409 to TripAlreadyActive`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"driver is not assigned to this vehicle"}"""))
        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"driver already has an active trip"}"""))
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, FakeTripStateStore(), FakeVehiclePrefsStore(), clock = { 0L })

        assertTrue(repo.start("bus-1", "5", "", "20260804").exceptionOrNull() is ApiError.NotAssigned)
        assertTrue(repo.start("bus-1", "5", "", "20260804").exceptionOrNull() is ApiError.TripAlreadyActive)
        server.shutdown()
    }

    @Test fun `trip end clears active trip`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"status":"trip ended"}"""))
        val store = FakeTripStateStore()
        store.saveActiveTrip(ActiveTrip(7L, "trip-0830", "bus-1", "5", "20260804", 100L))
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, store, FakeVehiclePrefsStore(), clock = { 0L })

        assertTrue(repo.end(7L).isSuccess)
        assertNull(store.activeTrip.first())
        server.shutdown()
    }

    @Test fun `trip start records the vehicle as recently used`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":9,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val prefs = FakeVehiclePrefsStore()
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, FakeTripStateStore(), prefs, clock = { 500L })

        assertTrue(repo.start("bus-1", "5", "", "20260804").isSuccess)

        assertEquals(listOf("bus-1"), prefs.recents.first())
        server.shutdown()
    }

    @Test fun `a trip start that fails records nothing as recently used`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"driver is not assigned to this vehicle"}"""))
        val prefs = FakeVehiclePrefsStore()
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, FakeTripStateStore(), prefs, clock = { 0L })

        assertTrue(repo.start("bus-1", "5", "", "20260804").isFailure)

        assertEquals(emptyList<String>(), prefs.recents.first())
        server.shutdown()
    }

    @Test fun `trip start still succeeds when the local prefs write fails`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":10,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val tripState = FakeTripStateStore()
        // The trip is already running on the server once recordUse is reached, so a disk error
        // there must not be reported to the driver as a failed start — their retry would 409.
        val failingPrefs = object : VehiclePrefsStore {
            override val favorites = MutableStateFlow(emptySet<String>())
            override val recents = MutableStateFlow(emptyList<String>())
            override suspend fun toggleFavorite(vehicleId: String) = Unit
            override suspend fun recordUse(vehicleId: String): Unit = throw IOException("disk full")
        }
        val repo = TripRepository(TrackerApiProvider { apiFor(server) }, tripState, failingPrefs, clock = { 500L })

        val result = repo.start("bus-1", "5", "", "20260804")

        assertTrue(result.isSuccess)
        assertNotNull(tripState.activeTrip.first())
        server.shutdown()
    }

    @Test fun `recent routes dedupe and cap at five`() = runTest {
        val store = FakeTripStateStore()
        for (r in listOf("1", "2", "3", "1", "4", "5", "6")) store.addRecentRoute(r)
        assertEquals(listOf("6", "5", "4", "1", "3"), store.recentRoutes.first())
    }

    // --- Schedule catalog: GET /api/v1/gtfs/routes and .../routes/{id}/trips ---

    private val twoRoutesJson =
        """{"routes":[{"id":"R1","short_name":"1","long_name":"Straight","color":"0077C0","text_color":"FFFFFF","type":3},""" +
            """{"id":"R2","short_name":"2","long_name":"Loop","color":"","text_color":"","type":3}]}"""

    private val oneTripJson =
        """{"route_id":"R1","service_date":"20260922","timezone":"Africa/Nairobi","trips":[""" +
            """{"id":"T1","headsign":"North","direction_id":null,"starts_at":"2026-09-22T07:02:00+03:00",""" +
            """"ends_at":"2026-09-22T07:40:00+03:00","first_stop":"Stop ST1","last_stop":"Stop ST3"}]}"""

    private fun catalogFor(server: MockWebServer) = CatalogRepository(TrackerApiProvider { apiFor(server) })

    @Test fun `catalog routes parse, including a route the feed gave no colour`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody(twoRoutesJson))

        val routes = catalogFor(server).routes().getOrThrow()

        assertEquals(listOf("R1", "R2"), routes.map { it.id })
        assertEquals("Straight", routes[0].longName)
        assertEquals("0077C0", routes[0].color)
        // route_color is optional in GTFS; "" has to survive as "" so the badge can fall back.
        assertEquals("", routes[1].color)
        assertEquals("", routes[1].textColor)
        server.shutdown()
    }

    @Test fun `catalog routes 404 with a text plain body means no schedule is loaded`() = runTest {
        val server = MockWebServer().apply { start() }
        // What net/http's default mux answers when GTFS_STATIC_URL is unset and the catalog
        // routes were never registered: not JSON, so the body must never be parsed.
        server.enqueue(
            MockResponse().setResponseCode(404)
                .setHeader("Content-Type", "text/plain; charset=utf-8")
                .setBody("404 page not found\n"),
        )

        val result = catalogFor(server).routes()

        assertTrue(result.exceptionOrNull() is ApiError.CatalogUnavailable)
        server.shutdown()
    }

    @Test fun `catalog routes 503 means no schedule is loaded yet`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(503).setBody("""{"error":"schedule data unavailable"}"""))

        val result = catalogFor(server).routes()

        assertTrue(result.exceptionOrNull() is ApiError.CatalogUnavailable)
        server.shutdown()
    }

    @Test fun `catalog routes 401 still maps to Unauthorized`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid token"}"""))

        val result = catalogFor(server).routes()

        assertTrue(result.exceptionOrNull() is ApiError.Unauthorized)
        server.shutdown()
    }

    @Test fun `catalog routes map a transport failure to a network error`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))

        val result = catalogFor(server).routes()

        assertEquals(ApiError.Other("network"), result.exceptionOrNull())
        server.shutdown()
    }

    @Test fun `catalog trips keep the wire timestamps and a null direction id`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody(oneTripJson))

        val page = catalogFor(server).trips("R1").getOrThrow()

        assertEquals("Africa/Nairobi", page.timezone)
        assertEquals("20260922", page.serviceDate)
        val trip = page.trips.single()
        assertEquals("T1", trip.id)
        assertNull(trip.directionId)
        // Parsed later, in the response's zone; the repository must not flatten the offset away.
        assertEquals("2026-09-22T07:02:00+03:00", trip.startsAt)
        assertEquals("Stop ST3", trip.lastStop)
        // No date parameter: the server picks today's service date in the agency's timezone.
        assertEquals("/api/v1/gtfs/routes/R1/trips", server.takeRequest().path)
        server.shutdown()
    }

    @Test fun `catalog trips percent-encode a route id containing a slash and a space`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody(oneTripJson))

        catalogFor(server).trips("1/A Main")

        // A GTFS route_id is arbitrary text. Both characters have to stay inside one path
        // segment or the request lands on a different route, or on no route at all.
        assertEquals("/api/v1/gtfs/routes/1%2FA%20Main/trips", server.takeRequest().path)
        server.shutdown()
    }

    @Test fun `catalog trips 404 is an unknown route, not a missing schedule`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"error":"unknown route"}"""))

        val result = catalogFor(server).trips("R9")

        // Falling back to manual entry here would be wrong: the routes call already succeeded,
        // so a schedule exists — this route just is not in it.
        assertTrue(result.exceptionOrNull() !is ApiError.CatalogUnavailable)
        assertTrue(result.exceptionOrNull() is ApiError.Other)
        server.shutdown()
    }

    // --- Cold-start / no-server-URL crash regression coverage (Task 8 review finding) ---

    @Test fun `ApiHolder seeds synchronously without waiting for the async session collector`() {
        // A scope whose Job is already cancelled: launchIn's collector never runs, simulating
        // the cold-start race where VehicleScreen's ViewModel is constructed before the
        // background sessionStore.session collector has delivered its first emission.
        val sessions = FakeSessionStore()
        sessions.state.value = Session("http://example.com", "jwt", 1L)
        val neverRunsScope = CoroutineScope(Job().apply { cancel() })
        val holder = ApiHolder(sessions, neverRunsScope)

        // Must not throw despite the async collector never having run.
        holder.api()
    }

    @Test fun `vehicle repository returns failure instead of throwing when session has no server url`() = runTest {
        val holder = ApiHolder(FakeSessionStore(), CoroutineScope(Job().apply { cancel() }))
        val repo = VehicleRepository(TrackerApiProvider(holder::api))

        val result = repo.myVehicles()

        assertTrue(result.isFailure)
        assertTrue(result.exceptionOrNull() is ApiError.Other)
    }

    @Test fun `trip repository returns failure instead of throwing when session has no server url`() = runTest {
        val holder = ApiHolder(FakeSessionStore(), CoroutineScope(Job().apply { cancel() }))
        val repo = TripRepository(TrackerApiProvider(holder::api), FakeTripStateStore(), FakeVehiclePrefsStore(), clock = { 0L })

        val result = repo.start("bus-1", "5", "trip-1", "20260804")

        assertTrue(result.isFailure)
        assertTrue(result.exceptionOrNull() is ApiError.Other)
    }
}
