package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.tripJson
import java.io.File

/**
 * `GET /api/v1/gtfs/trips/{trip_id}` through [CatalogRepository.trip], as the app reads it: the
 * cases of iOS's `TripGeometryTests`, and how each refusal maps.
 */
class TripGeometryTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private lateinit var server: MockWebServer
    private lateinit var catalog: CatalogRepository

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        catalog = CatalogRepository(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) })
    }

    @After fun tearDown() = server.shutdown()

    @Test fun `decodes the catalog payload`() = runTest {
        server.enqueue(MockResponse().setBody(tripJson()))

        val trip = catalog.trip("T1").getOrThrow()

        assertEquals("T1", trip.tripId)
        assertEquals("20260902", trip.serviceDate)
        assertEquals(TripFixtures.pacific, trip.timezone)
        assertEquals(listOf(GeoPoint(47.6, -122.33), GeoPoint(47.6045, -122.33), GeoPoint(47.609, -122.33)), trip.shapePoints)
        assertEquals(listOf("ST1", "ST3"), trip.stops.map { it.id })
        assertEquals("Stop ST3", trip.stops[1].name)
        assertEquals(47.609, trip.stops[1].lat, 0.0)
        assertEquals(1001.2, trip.stops[1].alongShapeM, 0.0)
        assertEquals(at(8, 0), trip.stops[0].departureAt)
        assertEquals("an after-midnight time lands on the next calendar day", at(1, 10, day = 3), trip.stops[1].arrivalAt)
        assertEquals(AdherenceThresholds(maxShapeDistanceM = 60.0, scheduleEarlyS = 900, scheduleLateS = 5400), trip.thresholds)
        assertEquals("/api/v1/gtfs/trips/T1", server.takeRequest().path)
    }

    @Test fun `a null direction id still decodes`() = runTest {
        server.enqueue(MockResponse().setBody(tripJson(directionId = "null")))

        assertEquals("T1", catalog.trip("T1").getOrThrow().tripId)
    }

    /**
     * The feed is not guaranteed to hand back well-formed pairs, and a three-element "point" must
     * not shift every later coordinate.
     */
    @Test fun `malformed shape pairs are dropped`() = runTest {
        server.enqueue(MockResponse().setBody(tripJson(points = "[[1,2],[3],[4,5,6],[7,8]]")))

        val trip = catalog.trip("T1").getOrThrow()

        assertEquals(listOf(GeoPoint(1.0, 2.0), GeoPoint(7.0, 8.0)), trip.shapePoints)
    }

    @Test fun `offset and Z timestamps both parse to the same instant`() = runTest {
        // Go writes the agency's offset unless the zone is UTC, when it writes Z instead.
        val stops = TripFixtures.T1_STOPS_JSON.replaceFirst("2026-09-02T08:00:00-07:00", "2026-09-02T15:00:00Z")
        server.enqueue(MockResponse().setBody(tripJson(stops = stops)))

        val trip = catalog.trip("T1").getOrThrow()

        assertEquals(at(8, 0), trip.stops[0].arrivalAt)
        assertEquals(trip.stops[0].arrivalAt, trip.stops[0].departureAt)
    }

    @Test fun `a fetched run round-trips through the file store unchanged`() = runTest {
        server.enqueue(MockResponse().setBody(tripJson()))
        val fetched = catalog.trip("T1").getOrThrow()
        val store = FileTripGeometryStore(File(tempFolder.newFolder(), "active_trip_geometry.json"))

        store.save(fetched)

        assertEquals(fetched, store.load())
    }

    @Test fun `a zone this platform cannot read is a failed fetch, not a crash`() = runTest {
        server.enqueue(MockResponse().setBody(tripJson().replace("America/Los_Angeles", "Not/AZone")))

        assertTrue(catalog.trip("T1").exceptionOrNull() is ApiError.Other)
    }

    @Test fun `trip ids are percent-encoded into one path segment`() = runTest {
        server.enqueue(MockResponse().setBody(tripJson()))

        catalog.trip("T1/A 2")

        assertEquals("/api/v1/gtfs/trips/T1%2FA%202", server.takeRequest().path)
    }

    @Test fun `404 is an unknown trip, not a missing schedule`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"error":"unknown trip"}"""))

        assertEquals(ApiError.Other("unknown trip"), catalog.trip("T9").exceptionOrNull())
    }

    @Test fun `422 means the run is not in service on the day the server resolves`() = runTest {
        server.enqueue(MockResponse().setResponseCode(422).setBody("""{"error":"trip not active on date"}"""))

        assertEquals(ApiError.TripNotActiveToday, catalog.trip("T1").exceptionOrNull())
    }

    @Test fun `503 means the schedule is not loaded`() = runTest {
        server.enqueue(MockResponse().setResponseCode(503).setBody("""{"error":"schedule data unavailable"}"""))

        assertEquals(ApiError.CatalogUnavailable, catalog.trip("T1").exceptionOrNull())
    }

    @Test fun `a transport failure is a network error`() = runTest {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))

        assertEquals(ApiError.Other("network"), catalog.trip("T1").exceptionOrNull())
    }
}
