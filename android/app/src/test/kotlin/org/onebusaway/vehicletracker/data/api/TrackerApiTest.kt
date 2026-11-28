package org.onebusaway.vehicletracker.data.api

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okio.Buffer
import okio.GzipSink
import okio.buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.engine.TripFixtures
import retrofit2.HttpException

class TrackerApiTest {
    private lateinit var server: MockWebServer
    private var token: String? = null
    private lateinit var api: TrackerApi

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        api = ApiFactory { token }.create(server.url("/").toString())
    }

    @After fun tearDown() = server.shutdown()

    @Test fun `login posts email and password`() = runTest {
        server.enqueue(MockResponse().setBody(
            """{"token":"jwt-abc","access_token":"jwt-abc","refresh_token":"a1b2c3","expires_in":900}"""))
        api.login(LoginRequest("d@example.com", "pw"))
        val recorded = server.takeRequest()
        assertEquals("/api/v1/auth/login", recorded.path)
        val sent = Json.parseToJsonElement(recorded.body.readUtf8()).jsonObject
        assertEquals(setOf("email", "password"), sent.keys)
    }

    // The shape POST /api/v1/auth/login and /auth/refresh both return
    // (newLoginResponse in auth_refresh.go), including the deprecated `token`
    // the server still sends for older clients.
    @Test fun `login parses the server's reply`() = runTest {
        server.enqueue(MockResponse().setBody(
            """{"token":"jwt-abc","access_token":"jwt-abc","refresh_token":"a1b2c3","expires_in":900}"""))
        val resp = api.login(LoginRequest("d@example.com", "pw"))
        assertEquals(LoginResponse(accessToken = "jwt-abc", refreshToken = "a1b2c3", expiresIn = 900), resp)
    }

    // POST /api/v1/auth/logout reads no body and answers 204 (handleLogout in auth.go).
    @Test fun `logout posts with the token and no body`() = runTest {
        token = "jwt-abc"
        server.enqueue(MockResponse().setResponseCode(204))

        api.logout()

        val recorded = server.takeRequest()
        assertEquals("/api/v1/auth/logout", recorded.path)
        assertEquals("POST", recorded.method)
        assertEquals("Bearer jwt-abc", recorded.getHeader("Authorization"))
        assertEquals(0L, recorded.bodySize)
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
            vehicleId = "bus-1", tripId = "trip-0830", routeId = "5", startDate = "20260804",
            latitude = -1.2921, longitude = 36.8219,
            bearing = null, speed = 8.5, accuracy = null, timestamp = 1752566400,
        ))
        val recorded = server.takeRequest()
        assertEquals("application/json", recorded.getHeader("Content-Type")?.substringBefore(";"))
        val sent = Json.parseToJsonElement(recorded.body.readUtf8()).jsonObject
        assertEquals(setOf("vehicle_id", "trip_id", "route_id", "start_date", "latitude", "longitude", "speed", "timestamp"), sent.keys)
        assertFalse(sent.containsKey("bearing"))
    }

    @Test fun `location report without a gtfs trip id omits trip_id entirely`() = runTest {
        token = "jwt"
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"status":"ok"}"""))
        api.postLocation(LocationReportDto(
            vehicleId = "bus-1", tripId = null, routeId = "5", startDate = "20260804",
            latitude = -1.2921, longitude = 36.8219, timestamp = 1752566400,
        ))
        val sent = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
        assertFalse(sent.containsKey("trip_id"))
        assertEquals("5", sent["route_id"]!!.jsonPrimitive.content)
        assertEquals("20260804", sent["start_date"]!!.jsonPrimitive.content)
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

    // A 307 or 308 keeps the method and the body, so a redirect followed would hand the password to whichever host
    // it names. The request to the other host would be answered, and the sign-in would work.
    @Test fun `a sign-in is not sent on where a redirect points`() = runTest {
        val elsewhere = MockWebServer().apply { start() }
        try {
            elsewhere.enqueue(MockResponse().setBody(
                """{"token":"jwt-abc","access_token":"jwt-abc","refresh_token":"a1b2c3","expires_in":900}"""))
            server.enqueue(MockResponse().setResponseCode(307).setHeader("Location", elsewhere.url("/api/v1/auth/login")))

            val error = runCatching { api.login(LoginRequest("d@example.com", "pw")) }.exceptionOrNull()

            assertEquals("the redirect comes back as the answer", 307, (error as? HttpException)?.code())
            assertEquals("nothing reached the other host", 0, elsewhere.requestCount)
        } finally {
            elsewhere.shutdown()
        }
    }

    @Test fun `a refresh is not sent on where a redirect points`() {
        val elsewhere = MockWebServer().apply { start() }
        try {
            elsewhere.enqueue(MockResponse().setBody(
                """{"token":"jwt-new","access_token":"jwt-new","refresh_token":"r2","expires_in":900}"""))
            server.enqueue(MockResponse().setResponseCode(308).setHeader("Location", elsewhere.url("/api/v1/auth/refresh")))

            val reply = ApiFactory { null }.createRefreshApi(server.url("/").toString()).refresh(RefreshRequest("r1")).execute()

            assertEquals(308, reply.code())
            assertEquals("nothing reached the other host", 0, elsewhere.requestCount)
        } finally {
            elsewhere.shutdown()
        }
    }

    @Test fun `base URL without trailing slash is normalized and still reaches the right path`() = runTest {
        val baseUrlWithoutSlash = server.url("/").toString().trimEnd('/')
        val apiFromUnslashedUrl = ApiFactory { token }.create(baseUrlWithoutSlash)
        server.enqueue(MockResponse().setBody("""[]"""))

        apiFromUnslashedUrl.myVehicles()

        assertEquals("/api/v1/vehicles", server.takeRequest().path)
    }

    // Since #692 the server gzips what it sends from /api/v1/gtfs/ and gives it an ETag, "private, no-cache" and
    // Vary: Accept-Encoding (compress.go). OkHttp asks for gzip and unzips the answer itself only while the app sets no
    // Accept-Encoding of its own. With no HTTP cache it keeps no answer, so it never sends If-None-Match and is never
    // answered 304 (#693). Giving the client a Cache would change that; its 304s would then need a test of their own.
    @Test fun `the timetable is read from gzipped answers, which are never revalidated`() = runTest {
        token = "jwt-abc"
        repeat(2) {
            server.enqueue(timetable("""{"routes":[{"id":"R1","short_name":"1","long_name":"Straight","color":"0077C0","text_color":"FFFFFF","type":3}]}"""))
            server.enqueue(timetable(
                """{"route_id":"R1","service_date":"20260902","timezone":"America/Los_Angeles","trips":[""" +
                    """{"id":"T1","headsign":"North","direction_id":0,"starts_at":"2026-09-02T08:00:00-07:00",""" +
                    """"ends_at":"2026-09-03T01:10:00-07:00","first_stop":"Stop ST1","last_stop":"Stop ST3"}]}""",
            ))
            server.enqueue(timetable(TripFixtures.tripJson()))
        }

        repeat(2) {
            assertEquals("R1", api.routes().routes.single().id)
            assertEquals("T1", api.routeTrips("R1").trips.single().id)
            assertEquals(3, api.trip("T1").shape.points.size)
        }

        assertEquals(6, server.requestCount)
        repeat(6) {
            val sent = server.takeRequest()
            assertEquals(sent.path, "gzip", sent.getHeader("Accept-Encoding"))
            assertNull(sent.path, sent.getHeader("If-None-Match"))
        }
    }

    /** A timetable answer as the server sends it since #692: gzipped, with an ETag the client may send back. */
    private fun timetable(json: String) = MockResponse()
        .setHeader("Content-Type", "application/json")
        .setHeader("Content-Encoding", "gzip")
        .setHeader("ETag", "W/\"590face42516f012\"")
        .setHeader("Cache-Control", "private, no-cache")
        .setHeader("Vary", "Accept-Encoding")
        .setBody(Buffer().also { body -> GzipSink(body).buffer().use { it.writeUtf8(json) } })
}
