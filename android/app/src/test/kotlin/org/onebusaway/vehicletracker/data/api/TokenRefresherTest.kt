package org.onebusaway.vehicletracker.data.api

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.onStart
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.FakeCryptor
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.VehicleRepository
import org.onebusaway.vehicletracker.di.ApiHolder
import retrofit2.HttpException
import java.io.File
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * Drives [TokenRefresher] the way the app does: through [ApiHolder] and the client [ApiFactory]
 * builds, against a [MockWebServer] that answers like the real one.
 *
 * Every [ApiHolder] here seeds its cached token once and never updates it, the furthest a copy can
 * lag the store. The refresher must work from the store regardless.
 */
class TokenRefresherTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val server = MockWebServer()
    private val fake = FakeServer()
    private val sessions = FakeSessionStore()
    private val scopes = mutableListOf<CoroutineScope>()
    private lateinit var serverUrl: String

    @Before fun setUp() {
        server.dispatcher = fake
        server.start()
        serverUrl = server.url("/").toString()
        sessions.state.value = Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900)
    }

    @After fun tearDown() {
        server.shutdown()
        scopes.forEach { it.cancel() }
    }

    private fun holder(store: SessionStore = sessions, now: Long = NOW) =
        ApiHolder(store, CoroutineScope(Job().apply { cancel() }), clock = { now })

    private fun newDataStore(): DataStore<Preferences> = PreferenceDataStoreFactory.create(
        scope = CoroutineScope(Job() + Dispatchers.IO).also { scopes += it },
        produceFile = { File(tempFolder.newFolder(), "session.preferences_pb") },
    )

    /** Every request the server got, in order. Call once, after the requests are done. */
    private fun recorded(): List<RecordedRequest> = List(server.requestCount) { server.takeRequest() }

    private fun List<RecordedRequest>.refreshes() = filter { it.path == "/api/v1/auth/refresh" }

    @Test fun `a 401 refreshes once and sends the request again with the new token`() = runTest {
        assertEquals(emptyList<VehicleDto>(), holder().api().myVehicles())

        val requests = recorded()
        assertEquals(3, requests.size)
        val (rejected, refresh, retry) = requests
        assertEquals("Bearer a1", rejected.getHeader("Authorization"))
        assertEquals("/api/v1/auth/refresh", refresh.path)
        assertEquals("""{"refresh_token":"r1"}""", refresh.body.readUtf8())
        assertEquals("application/json", refresh.getHeader("Content-Type")!!.toMediaType().let { "${it.type}/${it.subtype}" })
        assertNull("the refresh must not carry the access token", refresh.getHeader("Authorization"))
        assertEquals("Bearer a2", retry.getHeader("Authorization"))
        assertEquals(Session(serverUrl, "a2", NOW, "r2", NOW + 900), sessions.state.value)
    }

    // Issue #166's race: each report that gets a 401 refreshing on its own would present r1 twice,
    // and the second use revokes every refresh token the driver holds.
    @Test fun `two requests that get a 401 together refresh once`() = runBlocking {
        val bothRejected = CountDownLatch(2)
        fake.onUnauthorized = { bothRejected.countDown() }
        // Holds the new pair back until both requests have their 401, so both want a refresh at once.
        fake.refreshReply = { if (bothRejected.await(5, TimeUnit.SECONDS)) pair("a2", "r2") else MockResponse().setResponseCode(500) }
        val api = holder().api()

        val results = List(2) { async(Dispatchers.IO) { api.myVehicles() } }.awaitAll()

        assertEquals(List(2) { emptyList<VehicleDto>() }, results)
        val requests = recorded()
        assertEquals(1, requests.refreshes().size)
        assertEquals(2, requests.count { it.path == "/api/v1/vehicles" && it.getHeader("Authorization") == "Bearer a2" })
    }

    @Test fun `a 401 after another request has refreshed is sent again with the stored token, without refreshing`() = runTest {
        val holder = holder()
        holder.api() // seeds the cached copy with a1
        sessions.state.value = sessions.state.value.copy(token = "a2", refreshToken = "r2")

        holder.api().myVehicles()

        val requests = recorded()
        assertEquals(listOf("Bearer a1", "Bearer a2"), requests.map { it.getHeader("Authorization") })
        assertTrue(requests.refreshes().isEmpty())
    }

    @Test fun `a refused refresh drops the refresh token, keeps the access token, and the 401 reaches the caller`() = runTest {
        fake.refreshReply = { unauthorized("invalid or expired refresh token") }
        val holder = holder()
        val vehicles = VehicleRepository(TrackerApiProvider(holder::api))

        val result = vehicles.myVehicles()

        assertTrue("expected Unauthorized, got ${result.exceptionOrNull()}", result.exceptionOrNull() is ApiError.Unauthorized)
        assertEquals(Session(serverUrl, "a1", ISSUED_AT, null, ISSUED_AT + 900), sessions.state.value)

        val later = vehicles.myVehicles()

        assertTrue(later.exceptionOrNull() is ApiError.Unauthorized)
        assertEquals("nothing is left to refresh with", 1, recorded().refreshes().size)
    }

    @Test fun `a refresh that can't be reached fails the call as a network error and keeps both tokens`() =
        assertFailsAsNetworkErrorKeepingTokens(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST))

    @Test fun `a refresh the server fails fails the call as a network error and keeps both tokens`() =
        assertFailsAsNetworkErrorKeepingTokens(MockResponse().setResponseCode(500).setBody("""{"error":"internal server error"}"""))

    @Test fun `a rate-limited refresh fails the call as a network error and keeps both tokens`() =
        assertFailsAsNetworkErrorKeepingTokens(MockResponse().setResponseCode(429).setBody("""{"error":"too many attempts"}"""))

    // Called directly: through OkHttp, any exception the authenticator throws reaches the caller as
    // an IOException, so only a direct call shows it is one. On a phone, OkHttp also rethrows
    // anything else on its own thread, which ends the app.
    @Test fun `a 200 that is not a token pair fails as a network error and keeps both tokens`() {
        fake.refreshReply = { MockResponse().setBody("<html>Sign in to the Wi-Fi</html>") }
        val refresher = TokenRefresher(sessions, clock = { NOW }, cachedRefreshDueAt = { null }) { url ->
            ApiFactory { null }.createRefreshApi(url)
        }

        val failure = runCatching { refresher.afterUnauthorized("a1") }.exceptionOrNull()

        assertTrue("expected an IOException, got $failure", failure is IOException)
        assertEquals(Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900), sessions.state.value)
    }

    private fun assertFailsAsNetworkErrorKeepingTokens(refreshReply: MockResponse) = runTest {
        fake.refreshReply = { refreshReply }

        val failure = runCatching { holder().api().myVehicles() }.exceptionOrNull()

        assertTrue("expected an IOException, got $failure", failure is IOException)
        assertEquals(Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900), sessions.state.value)
    }

    @Test fun `a retry that gets a 401 again is not refreshed a second time`() = runTest {
        fake.accepted = "a token the server never issued"

        val failure = runCatching { holder().api().myVehicles() }.exceptionOrNull()

        assertEquals(401, (failure as? HttpException)?.code())
        val requests = recorded()
        assertEquals(listOf("Bearer a1", null, "Bearer a2"), requests.map { it.getHeader("Authorization") })
    }

    @Test fun `a wrong password with a session stored sends no token and refreshes nothing`() = runTest {
        val holder = holder()
        holder.api() // seeds the cached copy with a1, the token AuthInterceptor would otherwise send
        val auth = AuthRepository(sessions, holder.apiFactory, clock = { NOW })

        val result = auth.login(serverUrl, "d@example.com", "wrong")

        assertTrue(result.exceptionOrNull() is ApiError.Unauthorized)
        val login = recorded().single()
        assertEquals("/api/v1/auth/login", login.path)
        assertNull(login.getHeader("Authorization"))
        assertEquals("r1", sessions.state.value.refreshToken)
    }

    @Test fun `a refreshed pair that can't be saved drops the spent refresh token and still sends the request`() = runTest {
        val cryptor = FakeCryptor()
        val store = EncryptedSessionStore(newDataStore(), cryptor)
        store.saveLogin(serverUrl, "d@example.com", IssuedTokens("a1", "r1", ISSUED_AT + 900, ISSUED_AT))
        val holder = holder(store)
        cryptor.failEncrypt = true

        assertEquals(emptyList<VehicleDto>(), holder.api().myVehicles())

        val session = store.session.first()
        assertNull("r1 is spent and must never be presented again", session.refreshToken)
        assertEquals("a1", session.token)

        val later = runCatching { holder.api().myVehicles() }.exceptionOrNull()

        assertEquals(401, (later as? HttpException)?.code())
        val requests = recorded()
        assertEquals("Bearer a2", requests[2].getHeader("Authorization"))
        assertEquals(1, requests.refreshes().size)
    }

    @Test fun `a refreshed pair the disk can't take drops the spent refresh token and still sends the request`() = runTest {
        val diskFull = object : SessionStore by sessions {
            override suspend fun saveRefreshed(tokens: IssuedTokens) { throw IOException("No space left on device") }
        }

        assertEquals(emptyList<VehicleDto>(), holder(diskFull).api().myVehicles())

        assertNull("r1 is spent and must never be presented again", sessions.state.value.refreshToken)
    }

    @Test fun `a refresh token that can't be dropped still lets the 401 through`() = runTest {
        fake.refreshReply = { unauthorized("invalid or expired refresh token") }
        val readOnly = object : SessionStore by sessions {
            override suspend fun dropRefreshToken() { throw IOException("Read-only file system") }
        }

        val failure = runCatching { holder(readOnly).api().myVehicles() }.exceptionOrNull()

        assertEquals(401, (failure as? HttpException)?.code())
    }

    @Test fun `a token due a refresh is refreshed before the request, which never meets a 401`() = runTest {
        assertEquals(emptyList<VehicleDto>(), holder(now = DUE).api().myVehicles())

        val requests = recorded()
        assertEquals(listOf("/api/v1/auth/refresh", "/api/v1/vehicles"), requests.map { it.path })
        assertEquals("Bearer a2", requests[1].getHeader("Authorization"))
        assertEquals(Session(serverUrl, "a2", DUE, "r2", DUE + 900), sessions.state.value)
    }

    @Test fun `a request with no refresh due does not read the store`() {
        val reads = AtomicInteger()
        val counting = object : SessionStore by sessions {
            override val session = sessions.state.onStart { reads.incrementAndGet() }
        }
        val refresher = TokenRefresher(counting, clock = { DUE - 1 }, cachedRefreshDueAt = { DUE }) { url ->
            ApiFactory { null }.createRefreshApi(url)
        }

        assertEquals("a1", refresher.beforeExpiry("a1"))
        assertEquals(0, reads.get())
    }

    @Test fun `a token a second short of due is sent as it is`() = runTest {
        fake.accepted = "a1"

        holder(now = DUE - 1).api().myVehicles()

        assertEquals(listOf("Bearer a1"), recorded().map { it.getHeader("Authorization") })
    }

    @Test fun `an early refresh that can't get through still sends the request with the current token`() = runTest {
        fake.accepted = "a1"
        fake.refreshReply = { MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST) }

        assertEquals(emptyList<VehicleDto>(), holder(now = DUE).api().myVehicles())

        assertEquals("Bearer a1", recorded().last().getHeader("Authorization"))
        assertEquals(Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900), sessions.state.value)
    }

    // The token is dropped by the 401 that follows the access token's expiry, not here.
    @Test fun `an early refresh the server refuses still sends the request with the current token`() = runTest {
        fake.accepted = "a1"
        fake.refreshReply = { unauthorized("invalid or expired refresh token") }

        assertEquals(emptyList<VehicleDto>(), holder(now = DUE).api().myVehicles())

        assertEquals("Bearer a1", recorded().last().getHeader("Authorization"))
        assertEquals(Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900), sessions.state.value)
    }

    @Test fun `a token due a refresh that another request has already refreshed is swapped without a refresh`() = runTest {
        val holder = holder(now = DUE)
        holder.api() // seeds the cached copy with a1, due now
        sessions.state.value = Session(serverUrl, "a2", DUE, "r2", DUE + 900)

        holder.api().myVehicles()

        val requests = recorded()
        assertEquals(listOf("Bearer a2"), requests.map { it.getHeader("Authorization") })
        assertTrue(requests.refreshes().isEmpty())
    }

    @Test fun `a token saved after the app started is refreshed early too`() = runTest {
        sessions.state.value = Session(serverUrl, null, null)
        val holder = ApiHolder(sessions, CoroutineScope(Dispatchers.Unconfined).also { scopes += it }, clock = { DUE })
        sessions.state.value = Session(serverUrl, "a1", ISSUED_AT, "r1", ISSUED_AT + 900)

        holder.api().myVehicles()

        assertEquals(listOf("/api/v1/auth/refresh", "/api/v1/vehicles"), recorded().map { it.path })
    }

    @Test fun `the store, not the cached copy, decides whether a refresh is due`() = runTest {
        fake.accepted = "a1"
        val holder = holder(now = DUE)
        holder.api() // seeds the cached copy, which says a refresh is due now
        sessions.state.value = sessions.state.value.copy(accessExpiresAtEpochSec = ISSUED_AT + 3600)

        holder.api().myVehicles()

        assertTrue(recorded().refreshes().isEmpty())
    }

    /**
     * Answers like the server. The API takes only [accepted] and answers 401 to anything else.
     * `/auth/refresh` answers with [refreshReply], except to a refresh token it has already
     * exchanged, which it refuses: the server takes that for a stolen token.
     */
    private class FakeServer : Dispatcher() {
        @Volatile var accepted = "a2"
        @Volatile var refreshReply: () -> MockResponse = { pair("a2", "r2") }
        @Volatile var onUnauthorized: () -> Unit = {}
        private val spent = ConcurrentHashMap.newKeySet<String>()

        override fun dispatch(request: RecordedRequest): MockResponse = when {
            request.path == "/api/v1/auth/refresh" -> {
                val presented = Json.parseToJsonElement(request.body.clone().readUtf8())
                    .jsonObject.getValue("refresh_token").jsonPrimitive.content
                if (presented in spent) {
                    unauthorized("invalid or expired refresh token")
                } else {
                    refreshReply().also { if (it.handsOutTokens()) spent += presented }
                }
            }
            request.getHeader("Authorization") == "Bearer $accepted" -> MockResponse().setBody("[]")
            else -> unauthorized("invalid or expired token").also { onUnauthorized() }
        }

        private fun MockResponse.handsOutTokens() =
            status.contains(" 200 ") && socketPolicy == SocketPolicy.KEEP_OPEN
    }

    private companion object {
        const val ISSUED_AT = 1_785_886_200L
        const val NOW = ISSUED_AT + 600

        /** When a token issued at [ISSUED_AT] for 900 seconds is refreshed early: a minute before it expires. */
        const val DUE = ISSUED_AT + 840

        fun pair(access: String, refresh: String) = MockResponse().setBody(
            """{"token":"$access","access_token":"$access","refresh_token":"$refresh","expires_in":900}""")

        fun unauthorized(error: String) = MockResponse().setResponseCode(401).setBody("""{"error":"$error"}""")
    }
}
