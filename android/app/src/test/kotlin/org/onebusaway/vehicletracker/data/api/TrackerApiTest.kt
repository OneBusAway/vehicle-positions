package org.onebusaway.vehicletracker.data.api

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class TrackerApiTest {
    private lateinit var server: MockWebServer
    private var token: String? = null
    private lateinit var api: TrackerApi

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        api = ApiFactory { token }.create(server.url("/").toString())
    }

    @After fun tearDown() = server.shutdown()

    @Test fun `login posts email and password, parses token`() = runTest {
        server.enqueue(MockResponse().setBody("""{"token":"jwt-abc"}"""))
        val resp = api.login(LoginRequest("d@example.com", "pw"))
        assertEquals("jwt-abc", resp.token)
        val recorded = server.takeRequest()
        assertEquals("/api/v1/auth/login", recorded.path)
        val sent = Json.parseToJsonElement(recorded.body.readUtf8()).jsonObject
        assertEquals(setOf("email", "password"), sent.keys)
    }

    @Test fun `auth interceptor adds bearer token when present`() = runTest {
        token = "jwt-abc"
        server.enqueue(MockResponse().setBody("""[]"""))
        api.myVehicles()
        assertEquals("Bearer jwt-abc", server.takeRequest().getHeader("Authorization"))
    }

    @Test fun `no auth header when token absent`() = runTest {
        token = null
        server.enqueue(MockResponse().setBody("""[]"""))
        api.myVehicles()
        assertEquals(null, server.takeRequest().getHeader("Authorization"))
    }

    @Test fun `vehicles parses server response ignoring extra fields`() = runTest {
        server.enqueue(MockResponse().setBody(
            """[{"id":"bus-1","label":"Bus 1","agency_tag":"nairobi","active":true,
                 "created_at":"2026-08-01T00:00:00Z","updated_at":"2026-08-01T00:00:00Z"}]"""))
        val vehicles = api.myVehicles()
        assertEquals(listOf(VehicleDto("bus-1", "Bus 1")), vehicles)
    }

    @Test fun `location report omits null optionals and uses exact field names`() = runTest {
        token = "jwt"
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        api.postLocation(LocationReportDto(
            vehicleId = "bus-1", tripId = "route-5",
            latitude = -1.2921, longitude = 36.8219,
            bearing = null, speed = 8.5, accuracy = null, timestamp = 1752566400,
        ))
        val recorded = server.takeRequest()
        assertEquals("application/json", recorded.getHeader("Content-Type")?.substringBefore(";"))
        val sent = Json.parseToJsonElement(recorded.body.readUtf8()).jsonObject
        assertEquals(setOf("vehicle_id", "trip_id", "latitude", "longitude", "speed", "timestamp"), sent.keys)
        assertFalse(sent.containsKey("bearing"))
        assertTrue(sent.containsKey("speed"))
    }

    @Test fun `start trip sends snake_case fields and parses numeric trip id`() = runTest {
        token = "jwt"
        server.enqueue(MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":42,"vehicle_id":"bus-1","route_id":"5","gtfs_trip_id":"","start_time":"2026-08-04T08:30:00Z","status":"active"}"""))
        val trip = api.startTrip(StartTripRequest("bus-1", "5", ""))
        assertEquals(7L, trip.id)
        val sent = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertEquals(setOf("vehicle_id", "route_id", "gtfs_trip_id"), sent.keys)
    }
}
