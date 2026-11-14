package org.onebusaway.vehicletracker.ui

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.FakeServiceController
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripGeometryStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FakeVehiclePrefsStore
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.UnconfirmedEnd
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.signedInAs
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.tracking.TripEnder

/**
 * End trip with another driver signed in than the one who started the trip, as when the first driver's session ended
 * during the trip and the next driver signed in from the Tracking screen. The server ends a trip only for its own
 * driver (`EndTrip` in `store_trips.go`) and answers anyone else 404, which the app reads as a trip already ended.
 */
class EndAnotherDriversTripTest {
    private val server = MockWebServer()
    private val trips = FakeTripStateStore()
    private val geometry = FakeTripGeometryStore().apply { stored = TripFixtures.t1 }
    private val service = FakeServiceController()

    @Before fun setUp() = server.start()

    @After fun tearDown() = server.shutdown()

    private fun enderFor(sessions: SessionStore, store: TripStateStore = trips): TripEnder {
        val api = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
        return TripEnder(TripRepository(api, store, FakeVehiclePrefsStore(), sessions, clock = { 0L }), store, geometry, service, sessions)
    }

    private fun tripStartedBy(driverId: String?): ActiveTrip =
        ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L, driverId = driverId).also { trips.tripState.value = it }

    @Test fun `another driver's trip is not ended for good, and its end is kept for its own driver`() = runTest {
        // What the server answers an end from anyone but the trip's driver. The trip stays open there.
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"error":"active trip not found"}"""))
        val trip = tripStartedBy("1")
        val ender = enderFor(signedInAs("2"))

        ender.end(trip)

        assertTrue("reports stop on this phone either way", ender.state.value.ended)
        assertEquals(listOf(UnconfirmedEnd(7L, "1")), trips.unconfirmedEndsState.value)
        assertEquals("the server could only have answered 404", 0, server.requestCount)
        assertNull(trips.tripState.value)
        assertNull(geometry.stored)
        assertEquals(1, service.stopCount)
    }

    @Test fun `the trip's own driver still ends it on the server`() = runTest {
        server.enqueue(MockResponse().setBody("""{"status":"trip ended"}"""))
        val trip = tripStartedBy("1")
        val ender = enderFor(signedInAs("1"))

        ender.end(trip)

        assertTrue(ender.state.value.ended)
        // Counted before the request is taken, which would wait for ever for one that was never sent.
        assertEquals(1, server.requestCount)
        assertEquals("/api/v1/trips/end", server.takeRequest().path)
        assertEquals(emptyList<UnconfirmedEnd>(), trips.unconfirmedEndsState.value)
        assertNull(trips.tripState.value)
    }

    @Test fun `a trip stored before drivers were recorded still goes to the server under whoever is signed in`() = runTest {
        server.enqueue(MockResponse().setBody("""{"status":"trip ended"}"""))
        val trip = tripStartedBy(null)
        val ender = enderFor(signedInAs("2"))

        ender.end(trip)

        assertTrue(ender.state.value.ended)
        assertEquals(1, server.requestCount)
        assertEquals(emptyList<UnconfirmedEnd>(), trips.unconfirmedEndsState.value)
    }

    @Test fun `with nobody signed in the end still goes to the server, and its refusal is shown`() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"invalid token"}"""))
        val trip = tripStartedBy("1")
        val ender = enderFor(FakeSessionStore())

        ender.end(trip)

        assertFalse("an end the server refused must not leave the screen", ender.state.value.ended)
        assertEquals(1, server.requestCount)
        assertTrue(ender.state.value.failed)
        assertEquals(trip, trips.tripState.value)
        assertEquals(emptyList<UnconfirmedEnd>(), trips.unconfirmedEndsState.value)
    }

    @Test fun `a second end of another driver's trip while the first is being kept is ignored`() = runTest {
        val written = CompletableDeferred<Unit>()
        val trip = tripStartedBy("1")
        // Holds the first end at its write, as DataStore can, so the second tap lands while it is in flight.
        val slowTrips = object : TripStateStore by trips {
            override suspend fun endActiveTripLocally(signedInDriverId: String?) {
                written.await()
                trips.endActiveTripLocally(signedInDriverId)
            }
        }
        val ender = enderFor(signedInAs("2"), slowTrips)

        launch { ender.end(trip) }
        launch { ender.end(trip) }
        testScheduler.advanceUntilIdle()
        assertTrue("the buttons wait while it is written", ender.state.value.ending)
        written.complete(Unit)
        testScheduler.advanceUntilIdle()

        assertTrue(ender.state.value.ended)
        assertEquals(1, service.stopCount)
        assertFalse(ender.state.value.ending)
    }
}
