package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.di.ApiHolder
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ConcurrentLinkedQueue

/**
 * [PasswordRepository] through the app's own client and [org.onebusaway.vehicletracker.data.api.TokenRefresher], as
 * [org.onebusaway.vehicletracker.data.api.TokenRefresherTest] drives them, against a server that answers like the
 * real one.
 */
class PasswordRepositoryTest {
    private val server = MockWebServer()
    private val fake = FakeServer()
    private val sessions = FakeSessionStore()
    private lateinit var serverUrl: String

    @Before fun setUp() {
        server.dispatcher = fake
        server.start()
        serverUrl = server.url("/").toString()
        sessions.state.value = Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900)
    }

    @After fun tearDown() = server.shutdown()

    private fun repository(): PasswordRepository {
        val holder = ApiHolder(sessions, CoroutineScope(Job().apply { cancel() }), { NOW })
        return PasswordRepository(TrackerApiProvider(holder::api), holder.refreshTokens)
    }

    private fun recorded(): List<RecordedRequest> = List(server.requestCount) { server.takeRequest() }

    private fun List<RecordedRequest>.refreshes() = filter { it.path == "/api/v1/auth/refresh" }.map { it.field("refresh_token") }

    // The holder here never updates its copy of the access token, the furthest a copy can lag the store, so a request
    // after a refresh may go once with the old one first and be sent again by OkHttp. What the server answered the
    // password requests that reached it is what counts.
    private fun answered() = fake.passwordAnswered.filter { it != 401 }

    private fun RecordedRequest.field(name: String) =
        Json.parseToJsonElement(body.clone().readUtf8()).jsonObject.getValue(name).jsonPrimitive.content

    @Test fun `a change sends both passwords and this phone's refresh token, which it keeps`() = runTest {
        assertNull(repository().change("old-secret", "new-secret"))

        val sent = recorded().single()
        assertEquals("POST", sent.method)
        assertEquals("/api/v1/auth/password", sent.path)
        assertEquals("Bearer a1", sent.getHeader("Authorization"))
        assertEquals(listOf("old-secret", "new-secret", "r1"), listOf("current_password", "new_password", "refresh_token").map { sent.field(it) })
        assertEquals("the server keeps it good", "r1", sessions.state.value.refreshToken)
    }

    // The server answers 409 when the refresh token was spent, as by a refresh for another request.
    @Test fun `a 409 is refreshed, and the change sent once more with the new refresh token`() = runTest {
        fake.passwordAnswers += 409

        assertNull(repository().change("old-secret", "new-secret"))

        val sent = recorded()
        assertEquals(listOf("r1"), sent.refreshes())
        assertEquals(listOf(409, 204), answered())
        assertEquals("r2", sent.last().field("refresh_token"))
        assertEquals("Bearer a2", sent.last().getHeader("Authorization"))
    }

    @Test fun `a refresh token another request has already replaced is sent again without a refresh`() = runTest {
        fake.passwordAnswers += 409
        // Another request's refresh, saved as this one was on its way.
        fake.onPassword = { if (it.field("refresh_token") == "r1") sessions.state.value = sessions.state.value.copy(refreshToken = "r9") }

        assertNull(repository().change("old-secret", "new-secret"))

        val sent = recorded()
        assertEquals(listOf("/api/v1/auth/password", "/api/v1/auth/password"), sent.map { it.path })
        assertEquals("r9", sent[1].field("refresh_token"))
    }

    // OkHttp refreshes the expired access token and sends the same body again, with the refresh token that refresh spent.
    @Test fun `an access token that expired on the way is refreshed, and the change then names the new refresh token`() = runTest {
        fake.accepted = "a2"

        assertNull(repository().change("old-secret", "new-secret"))

        val sent = recorded()
        assertEquals("one refresh, OkHttp's after the 401", listOf("r1"), sent.refreshes())
        assertEquals("the same body again, naming the token that refresh spent, then the new one", listOf(409, 204), answered())
        assertEquals("r2", sent.last().field("refresh_token"))
        assertEquals("Bearer a2", sent.last().getHeader("Authorization"))
    }

    @Test fun `a second 409 is the caller's`() = runTest {
        fake.passwordAnswers += listOf(409, 409)

        assertEquals(PasswordChangeFailure.FAILED, repository().change("old-secret", "new-secret"))
        assertEquals(listOf("r1"), recorded().refreshes())
        assertEquals(listOf(409, 409), answered())
    }

    @Test fun `a refresh refused after a 409 ends the sign-in`() = runTest {
        fake.passwordAnswers += 409
        fake.refuseRefresh = true

        assertEquals(PasswordChangeFailure.SESSION_OVER, repository().change("old-secret", "new-secret"))
        assertNull("dropped, as after any refused refresh", sessions.state.value.refreshToken)
    }

    // With no refresh token this sign-in can't outlive the change.
    @Test fun `with no refresh token the sign-in is over, and nothing is sent`() = runTest {
        sessions.state.value = sessions.state.value.copy(refreshToken = null)

        assertEquals(PasswordChangeFailure.SESSION_OVER, repository().change("old-secret", "new-secret"))
        assertEquals(0, server.requestCount)
    }

    @Test fun `what the server says is told apart`() = runTest {
        for ((code, failure) in listOf(
            422 to PasswordChangeFailure.WRONG_CURRENT,
            429 to PasswordChangeFailure.TOO_MANY_TRIES,
            404 to PasswordChangeFailure.OLDER_SERVER,
            400 to PasswordChangeFailure.FAILED,
            403 to PasswordChangeFailure.FAILED,
            500 to PasswordChangeFailure.FAILED,
        )) {
            fake.passwordAnswers += code
            assertEquals("$code", failure, repository().change("old-secret", "new-secret"))
        }
        assertEquals("none of them refreshed", 0, recorded().count { it.path == "/api/v1/auth/refresh" })
    }

    @Test fun `a sign-in the server ended is over`() = runTest {
        fake.accepted = "a2"
        fake.refuseRefresh = true

        assertEquals(PasswordChangeFailure.SESSION_OVER, repository().change("old-secret", "new-secret"))
    }

    @Test fun `no answer is told apart`() = runTest {
        server.shutdown()

        assertEquals(PasswordChangeFailure.UNREACHABLE, repository().change("old-secret", "new-secret"))
    }

    private class FakeServer : Dispatcher() {
        @Volatile var accepted = "a1"
        @Volatile var refuseRefresh = false
        @Volatile var onPassword: (RecordedRequest) -> Unit = {}
        val passwordAnswers = ConcurrentLinkedQueue<Int>()

        /** What each password request got, in order, a 401 for a stale access token included. */
        val passwordAnswered = java.util.Collections.synchronizedList(mutableListOf<Int>())
        private val spent = ConcurrentHashMap.newKeySet<String>()

        override fun dispatch(request: RecordedRequest): MockResponse {
            val body = request.body.clone().readUtf8()
            fun field(name: String) = Json.parseToJsonElement(body).jsonObject.getValue(name).jsonPrimitive.content
            return when {
                request.path == "/api/v1/auth/refresh" -> {
                    val presented = field("refresh_token")
                    if (refuseRefresh || !spent.add(presented)) {
                        MockResponse().setResponseCode(401).setBody("""{"error":"invalid or expired refresh token"}""")
                    } else {
                        accepted = "a2"
                        MockResponse().setBody("""{"access_token":"a2","refresh_token":"r2","expires_in":900}""")
                    }
                }
                request.getHeader("Authorization") != "Bearer $accepted" -> {
                    if (request.path == "/api/v1/auth/password") passwordAnswered += 401
                    MockResponse().setResponseCode(401).setBody("""{"error":"invalid or expired token"}""")
                }
                request.path == "/api/v1/auth/password" -> {
                    onPassword(request)
                    val code = passwordAnswers.poll() ?: if (field("refresh_token") in spent) 409 else 204
                    passwordAnswered += code
                    if (code == 204) MockResponse().setResponseCode(204) else MockResponse().setResponseCode(code).setBody("""{"error":"refused"}""")
                }
                else -> MockResponse().setResponseCode(404)
            }
        }
    }

    private companion object {
        const val ISSUED_AT = 1_785_886_200L
        const val NOW = ISSUED_AT + 600
    }
}
