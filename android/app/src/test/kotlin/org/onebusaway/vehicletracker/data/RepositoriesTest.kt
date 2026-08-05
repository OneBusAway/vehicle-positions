package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.ApiFactory

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

    @Test fun `trip start saves active trip with gtfs id as location trip id`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"trip-0830","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(apiFor(server), store, clock = { 500L })

        val result = repo.start("bus-1", "5", "trip-0830")

        assertTrue(result.isSuccess)
        val trip = store.activeTrip.first()!!
        assertEquals(7L, trip.tripDbId)
        assertEquals("trip-0830", trip.locationTripId)
        assertEquals(listOf("5"), store.recentRoutes.first())
        server.shutdown()
    }

    @Test fun `trip start falls back to route id when gtfs id blank`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":8,"user_id":1,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(apiFor(server), store, clock = { 500L })

        repo.start("bus-1", "5", "")

        assertEquals("5", store.activeTrip.first()!!.locationTripId)
        server.shutdown()
    }

    @Test fun `trip start 403 maps to NotAssigned and 409 to TripAlreadyActive`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"driver is not assigned to this vehicle"}"""))
        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"driver already has an active trip"}"""))
        val repo = TripRepository(apiFor(server), FakeTripStateStore(), clock = { 0L })

        assertTrue(repo.start("bus-1", "5", "").exceptionOrNull() is ApiError.NotAssigned)
        assertTrue(repo.start("bus-1", "5", "").exceptionOrNull() is ApiError.TripAlreadyActive)
        server.shutdown()
    }

    @Test fun `trip end clears active trip`() = runTest {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"status":"trip ended"}"""))
        val store = FakeTripStateStore()
        store.saveActiveTrip(ActiveTrip(7L, "trip-0830", "bus-1", "5", 100L))
        val repo = TripRepository(apiFor(server), store, clock = { 0L })

        assertTrue(repo.end(7L).isSuccess)
        assertNull(store.activeTrip.first())
        server.shutdown()
    }

    @Test fun `recent routes dedupe and cap at five`() = runTest {
        val store = FakeTripStateStore()
        for (r in listOf("1", "2", "3", "1", "4", "5", "6")) store.addRecentRoute(r)
        assertEquals(listOf("6", "5", "4", "1", "3"), store.recentRoutes.first())
    }
}
