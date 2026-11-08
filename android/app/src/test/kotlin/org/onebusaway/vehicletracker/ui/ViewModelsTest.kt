package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
import androidx.compose.ui.graphics.Color
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import app.cash.turbine.test
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.onStart
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.internal.platform.Platform
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.*
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.account.AccountUiState
import org.onebusaway.vehicletracker.ui.account.AccountViewModel
import org.onebusaway.vehicletracker.ui.account.SIGN_OUT_WAIT_MS
import org.onebusaway.vehicletracker.ui.account.versionText
import org.onebusaway.vehicletracker.ui.login.LoginBanner
import org.onebusaway.vehicletracker.ui.login.LoginError
import org.onebusaway.vehicletracker.ui.login.LoginField
import org.onebusaway.vehicletracker.ui.login.LoginUiState
import org.onebusaway.vehicletracker.ui.login.LoginViewModel
import org.onebusaway.vehicletracker.ui.login.bannerOf
import org.onebusaway.vehicletracker.ui.login.fieldErrorRes
import org.onebusaway.vehicletracker.ui.login.serverAddressOf
import org.onebusaway.vehicletracker.ui.login.serverLabel
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.routes.RouteItem
import org.onebusaway.vehicletracker.ui.routes.RoutesUiState
import org.onebusaway.vehicletracker.ui.routes.RoutesViewModel
import org.onebusaway.vehicletracker.ui.routes.showsAsGrid
import org.onebusaway.vehicletracker.ui.runs.BoardRun
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.RunsViewModel
import org.onebusaway.vehicletracker.ui.runs.TripError
import org.onebusaway.vehicletracker.ui.runs.canSignOut
import org.onebusaway.vehicletracker.ui.resume.ResumeCard
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftViewModel
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import org.onebusaway.vehicletracker.ui.vehicles.BusSection
import org.onebusaway.vehicletracker.ui.vehicles.VehicleViewModel
import org.onebusaway.vehicletracker.ui.vehicles.VehiclesUiState
import retrofit2.HttpException
import retrofit2.Response
import java.io.File
import java.io.IOException
import java.net.UnknownServiceException
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

class ViewModelsTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)
    @After fun tearDown() = Dispatchers.resetMain()

    /**
     * Bounded, real-time poll for [condition], driving [dispatcher]'s queue on every
     * iteration. Needed for tests that go through a real `MockWebServer` request: Retrofit's
     * suspend calls resolve via OkHttp's own background thread pool, so the continuation
     * resumption is dispatched back onto [dispatcher] from a real thread at an unpredictable,
     * non-zero wall-clock time after the coroutine suspends — a single `advanceUntilIdle()`
     * call races that real completion and is not reliably sufficient. This polls instead of
     * sleeping a fixed duration, and fails loudly (rather than silently passing on a lucky
     * timing window, or hanging forever) if [condition] never becomes true within [timeoutMs].
     */
    private fun awaitCondition(timeoutMs: Long = 5_000, description: String, condition: () -> Boolean) {
        val deadlineNanos = System.nanoTime() + timeoutMs * 1_000_000
        while (System.nanoTime() < deadlineNanos) {
            dispatcher.scheduler.advanceUntilIdle()
            if (condition()) return
            Thread.sleep(5)
        }
        dispatcher.scheduler.advanceUntilIdle()
        if (!condition()) fail("Timed out after ${timeoutMs}ms waiting for: $description")
    }

    private fun authOver(sessionStore: SessionStore, apiProvider: TrackerApiProvider = TrackerApiProvider { error("no server to log out of") }) =
        AuthRepository(sessionStore, ApiFactory { null }, apiProvider, FakeOfflineFixQueue(), clock = { 0L })

    // --- Sign in ---

    private fun loginViewModel(
        sessionStore: SessionStore = FakeSessionStore(),
        tripState: FakeTripStateStore = FakeTripStateStore(),
        geometryStore: FakeTripGeometryStore = FakeTripGeometryStore(),
        managed: FakeManagedServerSource = FakeManagedServerSource(),
        serverSetup: ServerSetup = setupOver(sessionStore, tripState, managed),
        builtIn: String? = null,
        again: Boolean = false,
    ) = LoginViewModel(
        authOver(sessionStore),
        sessionStore,
        tripState,
        geometryStore,
        managed,
        serverSetup,
        builtIn,
        // As the nav graph writes it: only the route Tracking opens sets it.
        SavedStateHandle(if (again) mapOf(ARG_SIGN_IN_AGAIN to true) else emptyMap()),
    )

    private fun setupOver(
        sessionStore: SessionStore = FakeSessionStore(),
        tripState: FakeTripStateStore = FakeTripStateStore(),
        managed: FakeManagedServerSource = FakeManagedServerSource(),
    ) = ServerSetup(sessionStore, tripState, managed, debuggable = false, scope = CoroutineScope(dispatcher))

    private fun LoginViewModel.fill(server: String, email: String, password: String) {
        this.server.setTextAndPlaceCursorAtEnd(server)
        this.email.setTextAndPlaceCursorAtEnd(email)
        this.password.setTextAndPlaceCursorAtEnd(password)
    }

    @Test fun `sign in names the first empty field and calls no server`() = runTest(dispatcher) {
        val cases = listOf(
            Triple("", "", "") to LoginField.SERVER,
            Triple("http://localhost/", "", "pw") to LoginField.EMAIL,
            Triple("http://localhost/", "d@example.com", " ") to LoginField.PASSWORD,
        )
        for ((fields, missing) in cases) {
            val vm = loginViewModel()
            dispatcher.scheduler.advanceUntilIdle()
            vm.fill(fields.first, fields.second, fields.third)
            var succeeded = false
            vm.onSignIn { succeeded = true }
            dispatcher.scheduler.advanceUntilIdle()

            assertFalse(succeeded)
            assertEquals("for $fields", LoginError.MissingField(missing), vm.uiState.value.error)
            assertFalse(vm.uiState.value.loading)
        }
    }

    @Test fun `sign in succeeds and calls back`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""))
        val vm = loginViewModel()
        dispatcher.scheduler.advanceUntilIdle()
        vm.fill(server.url("/").toString(), "d@example.com", "pw")
        var succeeded = false
        vm.onSignIn { succeeded = true }
        assertTrue("signing in while the request is out", vm.uiState.value.loading)
        awaitCondition(description = "sign-in callback invoked") { succeeded }
        assertFalse(vm.uiState.value.loading)
        assertNull(vm.uiState.value.error)
        server.shutdown()
    }

    @Test fun `a second press while signing in sends nothing more`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""))
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""))
        val vm = loginViewModel()
        dispatcher.scheduler.advanceUntilIdle()
        vm.fill(server.url("/").toString(), "d@example.com", "pw")
        var successes = 0
        vm.onSignIn { successes++ }
        vm.onSignIn { successes++ }
        awaitCondition(description = "sign-in callback invoked") { successes > 0 }
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, successes)
        assertEquals(1, server.requestCount)
        server.shutdown()
    }

    private fun failedSignIn(response: MockResponse, server: MockWebServer = MockWebServer()): LoginUiState {
        server.start()
        server.enqueue(response)
        try {
            val vm = loginViewModel()
            dispatcher.scheduler.advanceUntilIdle()
            vm.fill(server.url("/").toString(), "d@example.com", "pw")
            vm.onSignIn { fail("a failed sign-in must not leave the screen") }
            awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }
            assertFalse(vm.uiState.value.loading)
            return vm.uiState.value
        } finally {
            server.shutdown()
        }
    }

    @Test fun `a refused password is the password's error`() = runTest(dispatcher) {
        val state = failedSignIn(MockResponse().setResponseCode(401).setBody("""{"error":"invalid email or password"}"""))
        assertEquals(LoginError.InvalidCredentials, state.error)
    }

    @Test fun `a server error is not blamed on the fields`() = runTest(dispatcher) {
        // Before, a 500 said "Enter a server URL, email, and password." with every field filled in.
        val state = failedSignIn(MockResponse().setResponseCode(500))
        assertEquals(LoginError.ServerError, state.error)
    }

    @Test fun `no answer is unreachable and opens the server field`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))
        val url = server.url("/").toString()
        // A returning driver, whose server is a quiet line until something is wrong with it.
        val vm = loginViewModel(FakeSessionStore().apply { state.value = Session(serverUrl = url, token = null, issuedAtEpochSec = null, email = "d@example.com") })
        dispatcher.scheduler.advanceUntilIdle()
        require(!vm.uiState.value.showsServerField)
        vm.password.setTextAndPlaceCursorAtEnd("pw")

        vm.onSignIn { fail("a failed sign-in must not leave the screen") }
        awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }

        assertEquals(LoginError.Unreachable(url), vm.uiState.value.error)
        assertTrue("the error goes under the server field, so it shows", vm.uiState.value.showsServerField)
        server.shutdown()
    }

    private fun assertNotATrackerServer(response: MockResponse) {
        val server = MockWebServer()
        val state = failedSignIn(response, server)
        assertEquals(LoginError.NotATrackerServer(server.url("/").toString()), state.error)
        assertTrue("the error goes under the server field, so it shows", state.changingServer)
    }

    @Test fun `a 404 is not a tracker server`() = runTest(dispatcher) {
        assertNotATrackerServer(MockResponse().setResponseCode(404).setBody("404 page not found"))
    }

    @Test fun `a 405 is not a tracker server`() = runTest(dispatcher) {
        assertNotATrackerServer(MockResponse().setResponseCode(405))
    }

    @Test fun `a web page is not a tracker server`() = runTest(dispatcher) {
        assertNotATrackerServer(MockResponse().setBody("<!doctype html><html><body>Sign in to the Wi-Fi</body></html>"))
    }

    @Test fun `a 200 with other JSON is not a tracker server`() = runTest(dispatcher) {
        assertNotATrackerServer(MockResponse().setBody("""{"status":"ok"}"""))
    }

    @Test fun `a 429 says wait, and doesn't open the server field`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(429).setBody("""{"error":"too many attempts"}"""))
        val url = server.url("/").toString()
        // A returning driver, whose server is a quiet line until something is wrong with it.
        val vm = loginViewModel(FakeSessionStore().apply { state.value = Session(serverUrl = url, token = null, issuedAtEpochSec = null, email = "d@example.com") })
        dispatcher.scheduler.advanceUntilIdle()
        require(!vm.uiState.value.showsServerField)
        vm.password.setTextAndPlaceCursorAtEnd("pw")

        vm.onSignIn { fail("a failed sign-in must not leave the screen") }
        awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }

        assertEquals(LoginError.TooManyAttempts, vm.uiState.value.error)
        assertFalse("nothing is wrong with the server", vm.uiState.value.showsServerField)
        assertEquals(url, vm.server.text.toString())
        assertEquals("d@example.com", vm.email.text.toString())
        assertEquals("pw", vm.password.text.toString())
        server.shutdown()
    }

    @Test fun `a 500 stays a server error`() = runTest(dispatcher) {
        // With a gateway's web page for a body, which a 5xx never gets as far as reading.
        for (status in listOf(500, 502, 503)) {
            val state = failedSignIn(MockResponse().setResponseCode(status).setBody("<html><body>Bad gateway</body></html>"))
            assertEquals("for $status", LoginError.ServerError, state.error)
            assertFalse("for $status", state.changingServer)
        }
    }

    @Test fun `a 400 is not blamed on the address`() = runTest(dispatcher) {
        // The server's own answer to a bad body, so it is a tracker server, and a bug if the app ever gets this.
        val state = failedSignIn(MockResponse().setResponseCode(400).setBody("""{"error":"invalid request body"}"""))
        assertEquals(LoginError.ServerError, state.error)
    }

    @Test fun `the catalog's and trips' errors don't change`() {
        for (status in listOf(404, 405, 429)) {
            val http = HttpException(Response.error<Unit>(status, "".toResponseBody()))
            assertTrue("for $status", mapHttpError(http) is ApiError.Other)
        }
        assertEquals(ApiError.Other("network"), mapHttpError(UnknownServiceException("CLEARTEXT")))
        assertTrue(mapHttpError(SerializationException("not json")) is ApiError.Other)
    }

    @Test fun `signing in again with a server that isn't one keeps the field closed and shows the banner`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(404))
        val url = server.url("/").toString()
        val vm = loginViewModel(
            sessionStore = FakeSessionStore().apply { state.value = Session(serverUrl = url, token = null, issuedAtEpochSec = null, email = "d@example.com") },
            tripState = FakeTripStateStore().apply { tripState.value = t1Trip },
            again = true,
        )
        dispatcher.scheduler.advanceUntilIdle()
        vm.password.setTextAndPlaceCursorAtEnd("pw")

        vm.onSignIn { fail("a failed sign-in must not leave the screen") }
        awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }

        assertEquals(LoginError.NotATrackerServer(url), vm.uiState.value.error)
        assertFalse("the open trip is on this server, so it cannot be changed here", vm.uiState.value.showsServerField)
        assertEquals(R.string.login_error_not_tracker, bannerOf(vm.uiState.value.error)?.text)
        server.shutdown()
    }

    @Test fun `a cleartext refusal maps to its own error, not to no network`() {
        assertEquals(ApiError.CleartextBlocked, mapLoginError(UnknownServiceException("CLEARTEXT communication to 192.0.2.1 not permitted by network security policy")))
    }

    @Test fun `the network policy's refusal says https is required`() = runTest(dispatcher) {
        // OkHttp asks its platform, which on a phone is the network security policy; on the JVM it always says yes, so
        // only that answer is faked here. CleartextPolicyTest runs the real policy on a device.
        Platform.resetForTests(object : Platform() {
            override fun isCleartextTrafficPermitted(hostname: String) = false
        })
        val server = MockWebServer().apply { start() }
        try {
            val vm = loginViewModel()
            dispatcher.scheduler.advanceUntilIdle()
            vm.fill(server.url("/").toString(), "d@example.com", "pw")

            vm.onSignIn { fail("a failed sign-in must not leave the screen") }
            awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }

            assertEquals(LoginError.HttpsRequired, vm.uiState.value.error)
            assertTrue("the error goes under the server field, so it shows", vm.uiState.value.changingServer)
            assertEquals("refused before it connected", 0, server.requestCount)
        } finally {
            Platform.resetForTests()
            server.shutdown()
        }
    }

    @Test fun `serverAddressOf reads what was typed`() {
        val cases = mapOf(
            "tracker.example.org" to "https://tracker.example.org/",
            " tracker.example.org " to "https://tracker.example.org/",
            "tracker.example.org:8443/" to "https://tracker.example.org:8443/",
            "http://10.0.2.2:8080" to "http://10.0.2.2:8080/",
            "HTTPS://Tracker.Example.org" to "https://tracker.example.org/",
            "foo bar" to null,
            "ftp://x" to null,
            "://" to null,
            "https://" to null,
            "" to null,
        )
        for ((typed, address) in cases) assertEquals("for '$typed'", address, serverAddressOf(typed))
    }

    @Test fun `an address with no scheme goes out over https`() = runTest(dispatcher) {
        // Plain http, so the handshake fails with no DNS involved, and a request sent as plain http would be answered.
        // Counted here, because requestCount also counts the handshake's bytes once the client gives up on them.
        val answered = AtomicInteger()
        val server = MockWebServer().apply {
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    answered.incrementAndGet()
                    return MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}""")
                }
            }
            start()
        }
        val vm = loginViewModel()
        dispatcher.scheduler.advanceUntilIdle()
        vm.fill("localhost:${server.port}", "d@example.com", "pw")

        vm.onSignIn { fail("nothing here speaks https") }
        // The server waits for a request line that never comes, so this takes the client's whole read timeout.
        awaitCondition(timeoutMs = 15_000, description = "sign-in failure surfaced") { vm.uiState.value.error != null }

        assertEquals(LoginError.Unreachable("https://localhost:${server.port}/"), vm.uiState.value.error)
        assertEquals(0, answered.get())
        server.shutdown()
    }

    @Test fun `an address that can't be one is refused without asking anyone`() = runTest(dispatcher) {
        // A returning driver, whose server is a quiet line until something is wrong with it.
        val vm = loginViewModel(FakeSessionStore().apply { state.value = Session(serverUrl = "https://saved.example.com", token = null, issuedAtEpochSec = null, email = "d@example.com") })
        dispatcher.scheduler.advanceUntilIdle()
        require(!vm.uiState.value.showsServerField)
        vm.fill("foo bar", "d@example.com", "pw")

        vm.onSignIn { fail("there is no server to sign in to") }
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(LoginError.InvalidAddress, vm.uiState.value.error)
        assertFalse(vm.uiState.value.loading)
        assertTrue("the error goes under the server field, so it shows", vm.uiState.value.showsServerField)
    }

    @Test fun `the address is saved as it was used`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""))
        val sessionStore = FakeSessionStore()
        val vm = loginViewModel(sessionStore)
        dispatcher.scheduler.advanceUntilIdle()
        vm.fill(" ${server.url("/")} ", "d@example.com", "pw")
        var succeeded = false

        vm.onSignIn { succeeded = true }
        awaitCondition(description = "sign-in callback invoked") { succeeded }

        assertEquals(server.url("/").toString(), sessionStore.state.value.serverUrl)
        server.shutdown()
    }

    @Test fun `typing clears the error`() = runTest(dispatcher) {
        val vm = loginViewModel()
        dispatcher.scheduler.advanceUntilIdle()
        vm.onSignIn { }
        assertEquals(LoginError.MissingField(LoginField.SERVER), vm.uiState.value.error)

        vm.onEdited()

        assertNull(vm.uiState.value.error)
    }

    @Test fun `a first sign-in shows the server field`() = runTest(dispatcher) {
        val vm = loginViewModel()
        dispatcher.scheduler.advanceUntilIdle()

        assertNull(vm.uiState.value.savedServer)
        assertTrue(vm.uiState.value.showsServerField)
        assertEquals("", vm.server.text.toString())
        assertEquals("", vm.email.text.toString())
    }

    @Test fun `a returning driver gets the server as a line and the email filled in`() = runTest(dispatcher) {
        val sessionStore = FakeSessionStore().apply {
            state.value = Session(serverUrl = "https://saved.example.com", token = null, issuedAtEpochSec = null, email = "maria@metro.example")
        }
        val vm = loginViewModel(sessionStore)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("https://saved.example.com", vm.uiState.value.savedServer)
        assertFalse(vm.uiState.value.showsServerField)
        assertEquals("https://saved.example.com", vm.server.text.toString())
        assertEquals("maria@metro.example", vm.email.text.toString())
        assertEquals("", vm.password.text.toString())

        vm.onChangeServer()

        assertTrue(vm.uiState.value.showsServerField)
    }

    @Test fun `filling in from the last sign-in keeps what the driver already typed`() = runTest(dispatcher) {
        val sessionStore = FakeSessionStore().apply {
            state.value = Session(serverUrl = "https://saved.example.com", token = null, issuedAtEpochSec = null, email = "maria@metro.example")
        }
        val vm = loginViewModel(sessionStore)
        vm.fill("https://typed.example.com", "typed@example.com", "")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("https://typed.example.com", vm.server.text.toString())
        assertEquals("typed@example.com", vm.email.text.toString())
    }

    // --- The server the agency sets ---

    private fun managedAs(url: String?, invalid: Boolean = false) =
        FakeManagedServerSource().apply { state.value = ManagedServer(url, invalid) }

    /** The store's first read lands after device management's, as a DataStore read does on a phone. */
    private fun slowToRead(store: FakeSessionStore): SessionStore = object : SessionStore by store {
        override val session = store.state.onStart { delay(1) }
    }

    @Test fun `the first of managed, saved and built-in is used`() = runTest(dispatcher) {
        val managed = "https://managed.example.org/"
        val saved = "https://saved.example.org/"
        val builtIn = "https://built-in.example.org/"
        data class Case(val managed: String?, val saved: String?, val builtIn: String?, val field: String, val showsField: Boolean)
        val cases = listOf(
            Case(managed, saved, builtIn, field = managed, showsField = false),
            Case(managed, saved, null, field = managed, showsField = false),
            Case(managed, null, builtIn, field = managed, showsField = false),
            Case(managed, null, null, field = managed, showsField = false),
            Case(null, saved, builtIn, field = saved, showsField = false),
            Case(null, saved, null, field = saved, showsField = false),
            Case(null, null, builtIn, field = builtIn, showsField = true),
            Case(null, null, null, field = "", showsField = true),
        )
        for (case in cases) {
            for (managedFirst in listOf(false, true)) {
                val store = FakeSessionStore().apply { state.value = Session(case.saved, null, null) }
                val vm = loginViewModel(
                    if (managedFirst) slowToRead(store) else store,
                    managed = managedAs(case.managed),
                    builtIn = case.builtIn,
                )
                dispatcher.scheduler.advanceUntilIdle()

                val label = "$case, ${if (managedFirst) "managed" else "saved"} read first"
                assertEquals(label, case.field, vm.server.text.toString())
                assertEquals(label, case.managed, vm.uiState.value.managedServer)
                assertEquals(label, case.saved, vm.uiState.value.savedServer)
                assertEquals(label, case.showsField, vm.uiState.value.showsServerField)
            }
        }
    }

    @Test fun `a managed server locks the field and is what signs in`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""))
        val managed = server.url("/").toString()
        val sessionStore = FakeSessionStore().apply {
            state.value = Session(serverUrl = "https://saved.example.org/", token = null, issuedAtEpochSec = null, email = "d@example.com")
        }
        val vm = loginViewModel(sessionStore, managed = managedAs(managed))
        dispatcher.scheduler.advanceUntilIdle()
        vm.onChangeServer()
        assertFalse("there is no Change, and nothing opens the field", vm.uiState.value.showsServerField)
        vm.password.setTextAndPlaceCursorAtEnd("pw")
        var succeeded = false

        vm.onSignIn { succeeded = true }
        awaitCondition(description = "sign-in callback invoked") { succeeded }

        assertEquals(1, server.requestCount)
        assertEquals(managed, sessionStore.state.value.serverUrl)
        server.shutdown()
    }

    @Test fun `a failed sign-in doesn't open a managed server's field`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))
        val managed = server.url("/").toString()
        val vm = loginViewModel(managed = managedAs(managed))
        dispatcher.scheduler.advanceUntilIdle()
        vm.fill(managed, "d@example.com", "pw")

        vm.onSignIn { fail("a failed sign-in must not leave the screen") }
        awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }

        assertEquals(LoginError.Unreachable(managed), vm.uiState.value.error)
        assertFalse("the driver can't change it", vm.uiState.value.changingServer)
        assertFalse(vm.uiState.value.showsServerField)
        assertEquals("the banner names the server instead", R.string.login_error_unreachable, bannerOf(vm.uiState.value.error)?.text)
        server.shutdown()
    }

    @Test fun `a managed server that changes while Sign in shows is picked up`() = runTest(dispatcher) {
        val managed = managedAs(null)
        val vm = loginViewModel(managed = managed)
        dispatcher.scheduler.advanceUntilIdle()
        vm.server.setTextAndPlaceCursorAtEnd("https://typed.example.org")

        managed.state.value = ManagedServer("https://a.example.org/", invalid = false)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("https://a.example.org/", vm.server.text.toString())
        assertFalse(vm.uiState.value.showsServerField)

        managed.state.value = ManagedServer("https://b.example.org/", invalid = false)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("https://b.example.org/", vm.server.text.toString())
        assertEquals("https://b.example.org/", vm.uiState.value.managedServer)

        managed.state.value = ManagedServer(null, invalid = true)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("the field is the driver's again, and empty", "", vm.server.text.toString())
        assertNull(vm.uiState.value.managedServer)
        assertTrue(vm.uiState.value.managedServerInvalid)
        assertTrue(vm.uiState.value.showsServerField)

        managed.state.value = ManagedServer(null, invalid = false)
        dispatcher.scheduler.advanceUntilIdle()
        assertFalse(vm.uiState.value.managedServerInvalid)
    }

    @Test fun `a managed server that goes away leaves the saved one`() = runTest(dispatcher) {
        val managed = managedAs("https://managed.example.org/")
        val sessionStore = FakeSessionStore().apply { state.value = Session("https://saved.example.org/", null, null) }
        val vm = loginViewModel(sessionStore, managed = managed)
        dispatcher.scheduler.advanceUntilIdle()

        managed.state.value = ManagedServer(null, invalid = false)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("https://saved.example.org/", vm.server.text.toString())
        assertFalse("the saved server is a line again", vm.uiState.value.showsServerField)
    }

    @Test fun `the built-in address fills only an empty field`() = runTest(dispatcher) {
        val builtIn = "https://built-in.example.org/"

        val fresh = loginViewModel(builtIn = builtIn)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(builtIn, fresh.server.text.toString())
        assertNull("it is not the phone's server until a sign-in", fresh.uiState.value.savedServer)
        assertTrue("the driver can still change it", fresh.uiState.value.showsServerField)

        val returning = loginViewModel(FakeSessionStore().apply { state.value = Session("https://saved.example.org/", null, null) }, builtIn = builtIn)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("https://saved.example.org/", returning.server.text.toString())

        val typing = loginViewModel(builtIn = builtIn)
        typing.server.setTextAndPlaceCursorAtEnd("https://typed.example.org")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("https://typed.example.org", typing.server.text.toString())
    }

    @Test fun `a managed server that goes away leaves the built-in one`() = runTest(dispatcher) {
        val managed = managedAs("https://managed.example.org/")
        val vm = loginViewModel(managed = managed, builtIn = "https://built-in.example.org/")
        dispatcher.scheduler.advanceUntilIdle()

        managed.state.value = ManagedServer(null, invalid = false)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("https://built-in.example.org/", vm.server.text.toString())
        assertTrue(vm.uiState.value.showsServerField)
    }

    // --- A setup link ---

    private val setupLink = "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org"
    private val serverInLink = "https://tracker.example.org/"

    /** The driver opens the link and agrees to it. */
    private fun ServerSetup.agree() {
        open(setupLink)
        dispatcher.scheduler.advanceUntilIdle()
        require(prompt.value == ServerSetupLink.Decision.Confirm(serverInLink, "tracker.example.org")) { "asked ${prompt.value}" }
        confirm(serverInLink)
        dispatcher.scheduler.advanceUntilIdle()
    }

    @Test fun `a confirmed link fills the field, and leaving Sign in forgets it`() = runTest(dispatcher) {
        val sessionStore = FakeSessionStore()
        val setup = setupOver(sessionStore)
        val vm = loginViewModel(sessionStore, serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()

        setup.agree()

        assertEquals(serverInLink, vm.server.text.toString())
        assertTrue(vm.uiState.value.showsServerField)
        assertNull("saved by nothing but a sign-in", sessionStore.state.value.serverUrl)
        val next = loginViewModel(sessionStore, serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("", next.server.text.toString())
    }

    @Test fun `a confirmed link opens the field over the saved server`() = runTest(dispatcher) {
        val sessionStore = FakeSessionStore().apply {
            state.value = Session(serverUrl = "https://saved.example.org/", token = null, issuedAtEpochSec = null, email = "d@example.com")
        }
        val setup = setupOver(sessionStore)
        val vm = loginViewModel(sessionStore, serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()
        require(!vm.uiState.value.showsServerField)
        vm.onSignIn { fail("there is no password") }
        require(vm.uiState.value.error == LoginError.MissingField(LoginField.PASSWORD))

        setup.agree()

        assertEquals(serverInLink, vm.server.text.toString())
        assertTrue(vm.uiState.value.showsServerField)
        assertNull("the last error was about something else", vm.uiState.value.error)
        assertEquals("https://saved.example.org/", sessionStore.state.value.serverUrl)
    }

    @Test fun `agreeing to the same link twice fills it twice`() = runTest(dispatcher) {
        val setup = setupOver()
        val vm = loginViewModel(serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()
        setup.agree()
        vm.server.setTextAndPlaceCursorAtEnd("https://typed.example.org")

        setup.agree()

        assertEquals(serverInLink, vm.server.text.toString())
    }

    @Test fun `a link, then a sign-in somewhere else, saves only what was signed in to`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""))
        val sessionStore = FakeSessionStore()
        val setup = setupOver(sessionStore)
        val vm = loginViewModel(sessionStore, serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()
        setup.agree()
        vm.fill(server.url("/").toString(), "d@example.com", "pw")
        var succeeded = false

        vm.onSignIn { succeeded = true }
        awaitCondition(description = "sign-in callback invoked") { succeeded }

        assertEquals(server.url("/").toString(), sessionStore.state.value.serverUrl)
        server.shutdown()
    }

    @Test fun `a token left on the phone is never paired with a link's host`() = runTest(dispatcher) {
        // Sign in shows, because the token has expired with no refresh token, but it is still stored.
        val session = Session(serverUrl = "https://positions.example.org/", token = jwtFor("7"), issuedAtEpochSec = 0L, email = "d@example.com")
        val sessionStore = FakeSessionStore().apply { state.value = session }
        val setup = setupOver(sessionStore)
        val vm = loginViewModel(sessionStore, serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()

        setup.open(setupLink)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(ServerSetupLink.Decision.RefusedSignedIn("positions.example.org"), setup.prompt.value)
        // Agreed to all the same, as a dialog left up from before could be.
        setup.confirm(serverInLink)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("https://positions.example.org/", vm.server.text.toString())
        assertEquals(session, sessionStore.state.value)
    }

    @Test fun `a link can't replace the agency's server`() = runTest(dispatcher) {
        // The link was agreed to just before the agency set the server.
        val setup = setupOver()
        val vm = loginViewModel(managed = managedAs("https://managed.example.org/"), serverSetup = setup)
        dispatcher.scheduler.advanceUntilIdle()

        setup.agree()

        assertEquals("https://managed.example.org/", vm.server.text.toString())
        assertNull("used up, so it can't fill the field later", setup.linkedServer.value)
    }

    @Test fun `signing in again ignores a linked server`() = runTest(dispatcher) {
        // Agreed to on a phone where nothing refuses it, so only Sign in can.
        val setup = setupOver()
        val vm = loginViewModel(
            sessionStore = FakeSessionStore().apply { state.value = Session(serverUrl = "https://saved.example.org/", token = null, issuedAtEpochSec = null, email = "d@example.com") },
            tripState = FakeTripStateStore().apply { tripState.value = t1Trip },
            serverSetup = setup,
            again = true,
        )
        dispatcher.scheduler.advanceUntilIdle()

        setup.agree()

        assertEquals("the open trip is on the saved server", "https://saved.example.org/", vm.server.text.toString())
        assertFalse(vm.uiState.value.showsServerField)
    }

    @Test fun `signing in again ignores the managed server`() = runTest(dispatcher) {
        val vm = loginViewModel(
            sessionStore = FakeSessionStore().apply { state.value = Session(serverUrl = "https://saved.example.org/", token = null, issuedAtEpochSec = null, email = "d@example.com") },
            tripState = FakeTripStateStore().apply { tripState.value = t1Trip },
            managed = managedAs("https://managed.example.org/"),
            again = true,
        )
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("the open trip is on the saved server", "https://saved.example.org/", vm.server.text.toString())
        assertNull(vm.uiState.value.managedServer)
    }

    @Test fun `signing in again shows the open trip's route and who signed in last`() = runTest(dispatcher) {
        val vm = loginViewModel(
            sessionStore = FakeSessionStore().apply { state.value = Session(serverUrl = "https://saved.example.com", token = null, issuedAtEpochSec = null, email = "maria@metro.example") },
            tripState = FakeTripStateStore().apply { tripState.value = t1Trip },
            geometryStore = FakeTripGeometryStore().apply {
                stored = TripFixtures.t1.copy(routeShortName = "7", routeColor = "FDB71A", routeTextColor = "000000")
            },
            again = true,
        )
        dispatcher.scheduler.advanceUntilIdle()

        val state = vm.uiState.value
        assertTrue(state.again)
        assertEquals(RouteBadgeModel.of("7", "FDB71A", "000000"), state.openTrip)
        assertEquals("maria@metro.example", state.savedEmail)
        assertEquals("maria@metro.example", vm.email.text.toString())
    }

    @Test fun `signing in again with no schedule on the phone names the route by its id`() = runTest(dispatcher) {
        val vm = loginViewModel(tripState = FakeTripStateStore().apply { tripState.value = t1Trip }, again = true)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(RouteBadgeModel("R1", null, null), vm.uiState.value.openTrip)
    }

    @Test fun `signing in again keeps the server closed when it cannot be reached`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))
        val url = server.url("/").toString()
        val vm = loginViewModel(
            sessionStore = FakeSessionStore().apply { state.value = Session(serverUrl = url, token = null, issuedAtEpochSec = null, email = "d@example.com") },
            tripState = FakeTripStateStore().apply { tripState.value = t1Trip },
            again = true,
        )
        dispatcher.scheduler.advanceUntilIdle()
        vm.password.setTextAndPlaceCursorAtEnd("pw")

        vm.onSignIn { fail("a failed sign-in must not leave the screen") }
        awaitCondition(description = "sign-in failure surfaced") { vm.uiState.value.error != null }

        assertEquals(LoginError.Unreachable(url), vm.uiState.value.error)
        assertFalse("the open trip is on this server, so it cannot be changed here", vm.uiState.value.showsServerField)
        server.shutdown()
    }

    @Test fun `a first sign-in looks for no trip`() = runTest(dispatcher) {
        val vm = loginViewModel(
            tripState = FakeTripStateStore().apply { tripState.value = t1Trip },
            geometryStore = FakeTripGeometryStore().apply { stored = TripFixtures.t1.copy(routeShortName = "7") },
        )
        dispatcher.scheduler.advanceUntilIdle()

        assertFalse(vm.uiState.value.again)
        assertNull(vm.uiState.value.openTrip)
    }

    @Test fun `the server reads as its host, with a port only when it is not the usual one`() {
        assertEquals("tracker.example.org", serverLabel("https://tracker.example.org/"))
        assertEquals("tracker.example.org", serverLabel("http://tracker.example.org:80"))
        assertEquals("10.0.2.2:8081", serverLabel("http://10.0.2.2:8081"))
        assertEquals("tracker.example.org", serverLabel(" tracker.example.org "))
    }

    @Test fun `each error shows under the field it is about, or above the button`() {
        for (field in LoginField.entries) {
            assertNull(fieldErrorRes(null, field))
            assertNull(fieldErrorRes(LoginError.ServerError, field))
            assertNull(fieldErrorRes(LoginError.TooManyAttempts, field))
        }
        assertEquals(R.string.login_error_missing_server, fieldErrorRes(LoginError.MissingField(LoginField.SERVER), LoginField.SERVER))
        assertEquals(R.string.login_error_missing_email, fieldErrorRes(LoginError.MissingField(LoginField.EMAIL), LoginField.EMAIL))
        assertEquals(R.string.login_error_missing_password, fieldErrorRes(LoginError.MissingField(LoginField.PASSWORD), LoginField.PASSWORD))
        assertNull(fieldErrorRes(LoginError.MissingField(LoginField.EMAIL), LoginField.PASSWORD))
        assertEquals(R.string.login_error_invalid_credentials, fieldErrorRes(LoginError.InvalidCredentials, LoginField.PASSWORD))
        assertNull(fieldErrorRes(LoginError.InvalidCredentials, LoginField.EMAIL))
        assertEquals(R.string.login_error_invalid_address, fieldErrorRes(LoginError.InvalidAddress, LoginField.SERVER))
        assertNull(fieldErrorRes(LoginError.InvalidAddress, LoginField.EMAIL))
        assertEquals(R.string.login_error_https_required, fieldErrorRes(LoginError.HttpsRequired, LoginField.SERVER))
        assertNull(fieldErrorRes(LoginError.HttpsRequired, LoginField.PASSWORD))
        assertEquals(R.string.login_error_check_address, fieldErrorRes(LoginError.NotATrackerServer("x"), LoginField.SERVER))
        assertNull(fieldErrorRes(LoginError.NotATrackerServer("x"), LoginField.PASSWORD))
        assertEquals(R.string.login_error_check_address, fieldErrorRes(LoginError.Unreachable("x"), LoginField.SERVER))
        assertNull(fieldErrorRes(LoginError.Unreachable("x"), LoginField.PASSWORD))

        assertEquals(R.string.login_error_unreachable, bannerOf(LoginError.Unreachable("x"))?.text)
        assertEquals(R.string.login_error_server, bannerOf(LoginError.ServerError)?.text)
        assertEquals(LoginBanner(R.drawable.ic_dns, R.string.login_error_not_tracker), bannerOf(LoginError.NotATrackerServer("x")))
        assertEquals(LoginBanner(R.drawable.ic_error, R.string.login_error_too_many_tries), bannerOf(LoginError.TooManyAttempts))
        assertNull(bannerOf(LoginError.HttpsRequired))
        assertNull(bannerOf(LoginError.InvalidCredentials))
        assertNull(bannerOf(LoginError.InvalidAddress))
        assertNull(bannerOf(LoginError.MissingField(LoginField.EMAIL)))
        assertNull(bannerOf(null))
    }

    // --- Launch: where the app lands, and whether a stored trip is still being reported ---

    private val launchNow = 100_000L
    private val freshSession = Session("https://tracker.example.com", "jwt", launchNow - 3600)

    /** The production value. The dispatcher's clock is virtual, so no test here waits it out. */
    private val serviceAnnounceGrace = Duration.ofSeconds(2)

    private fun appNavViewModel(
        trip: ActiveTrip? = null,
        session: Session = freshSession,
        tracking: TrackingRepository = TrackingRepository(),
        sessionStore: SessionStore = FakeSessionStore().apply { state.value = session },
        queue: OfflineFixQueue = FakeOfflineFixQueue(),
    ) = AppNavViewModel(
        sessionStore,
        FakeTripStateStore().apply { tripState.value = trip },
        tracking,
        queue,
        clock = { launchNow },
        serviceAnnounceGrace = serviceAnnounceGrace,
    )

    @Test fun noTrip_andFreshToken_goesToVehicles() = runTest(dispatcher) {
        val vm = appNavViewModel()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("vehicles", vm.startDestination.value)
    }

    @Test fun noTrip_andStaleToken_goesToLogin() = runTest(dispatcher) {
        val vm = appNavViewModel(session = freshSession.copy(issuedAtEpochSec = launchNow - 25 * 3600))
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("login", vm.startDestination.value)
    }

    // Signed in a day and an hour ago: the 24 hours the old check allowed are over too.
    @Test fun noTrip_andExpiredToken_withRefreshToken_goesToVehicles() = runTest(dispatcher) {
        val vm = appNavViewModel(
            session = freshSession.copy(
                issuedAtEpochSec = launchNow - 25 * 3600,
                refreshToken = "r1",
                accessExpiresAtEpochSec = launchNow - 3600,
            ),
        )
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("vehicles", vm.startDestination.value)
    }

    @Test fun storedTrip_withServiceActive_goesStraightToTracking() = runTest(dispatcher) {
        // The shift is still reporting: the task was swiped away, or START_STICKY restarted the
        // service. Prompting here would interrupt a healthy shift every time the app is opened.
        val tracking = TrackingRepository().apply { update { it.copy(active = true) } }
        val vm = appNavViewModel(trip = t1Trip, tracking = tracking)
        // No virtual time passes: a running service is taken at its word, with no wait.
        dispatcher.scheduler.runCurrent()

        assertEquals("tracking", vm.startDestination.value)
    }

    @Test fun storedTrip_withNoService_goesToResume() = runTest(dispatcher) {
        val vm = appNavViewModel(trip = t1Trip)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("resume", vm.startDestination.value)
    }

    @Test fun storedTrip_serviceAnnouncesLate_goesToTracking() = runTest(dispatcher) {
        val tracking = TrackingRepository()
        val vm = appNavViewModel(trip = t1Trip, tracking = tracking)
        dispatcher.scheduler.advanceTimeBy(serviceAnnounceGrace.toMillis() - 1)
        dispatcher.scheduler.runCurrent()
        assertNull("still waiting on the service", vm.startDestination.value)

        tracking.update { it.copy(active = true) }
        dispatcher.scheduler.runCurrent()

        assertEquals("tracking", vm.startDestination.value)
    }

    @Test fun storedTrip_serviceNeverAnnounces_goesToResume() = runTest(dispatcher) {
        val vm = appNavViewModel(trip = t1Trip)
        dispatcher.scheduler.advanceTimeBy(serviceAnnounceGrace.toMillis() - 1)
        dispatcher.scheduler.runCurrent()
        assertNull("the prompt waits out the whole grace window", vm.startDestination.value)

        dispatcher.scheduler.advanceTimeBy(1)
        dispatcher.scheduler.runCurrent()

        assertEquals("resume", vm.startDestination.value)
    }

    // --- Launch: an offline queue a build before owners left without one (#237) ---

    private val queuedFix = LocationReportDto(
        vehicleId = "bus-1", routeId = "R1", startDate = "20260804", latitude = 1.0, longitude = 2.0, timestamp = 1L,
    )

    private fun unownedQueue() = FakeOfflineFixQueue().apply { queued.addLast(queuedFix) }

    private suspend fun signIn(queue: OfflineFixQueue, server: MockWebServer, email: String) {
        server.enqueue(MockResponse().setBody("""{"access_token":"jwt","refresh_token":"r","expires_in":900}"""))
        val auth = AuthRepository(FakeSessionStore(), ApiFactory { null }, TrackerApiProvider { error("no logout") }, queue, clock = { 0L })
        val result = auth.login(server.url("/").toString(), email, "pw")
        assertTrue("the sign-in itself must succeed: ${result.exceptionOrNull()}", result.isSuccess)
    }

    // Saved without the slash and in another case than the sign-in types it: the two still name one account.
    @Test fun `a restored session keeps its unowned fixes through its driver's next sign-in`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        val queue = unownedQueue()
        val serverUrl = server.url("/").toString().trimEnd('/')
        appNavViewModel(session = freshSession.copy(serverUrl = serverUrl, email = "D@example.com"), queue = queue)
        dispatcher.scheduler.advanceUntilIdle()

        signIn(queue, server, " d@example.com")

        assertEquals(listOf(queuedFix), queue.queued.toList())
        server.shutdown()
    }

    @Test fun `another driver signing in still drops a restored session's fixes`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        val queue = unownedQueue()
        appNavViewModel(session = freshSession.copy(serverUrl = server.url("/").toString(), email = "d@example.com"), queue = queue)
        dispatcher.scheduler.advanceUntilIdle()

        signIn(queue, server, "e@example.com")

        assertEquals(emptyList<LocationReportDto>(), queue.queued.toList())
        server.shutdown()
    }

    @Test fun `launch leaves a queue that has an owner alone`() = runTest(dispatcher) {
        val owner = "https://other.example e@example.com"
        val queue = unownedQueue().apply { this.owner = owner }
        appNavViewModel(session = freshSession.copy(email = "d@example.com"), queue = queue)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(owner, queue.owner)
        assertEquals(listOf(queuedFix), queue.queued.toList())
    }

    @Test fun `a restored session with no email claims nothing`() = runTest(dispatcher) {
        val queue = unownedQueue()
        val vm = appNavViewModel(session = freshSession.copy(email = null), queue = queue)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("the launch finished", "vehicles", vm.startDestination.value)

        assertNull(queue.owner)
        assertEquals(listOf(queuedFix), queue.queued.toList())
    }

    // --- Vehicle picker: search, favorites and recents (#36) ---

    /** Deliberately not in display order, so the ordering assertions below mean something. */
    private val threeVehiclesJson =
        """[{"id":"bus-3","label":"Night Owl"},{"id":"van-2","label":"Airport Shuttle"},{"id":"bus-1","label":"Downtown Express"}]"""

    /**
     * Builds a [VehicleViewModel] over a `MockWebServer` serving [body] from
     * `GET /api/v1/vehicles`, waits for the load to land, and runs [block] against it.
     */
    private fun withVehicleViewModel(
        body: String = threeVehiclesJson,
        prefs: FakeVehiclePrefsStore = FakeVehiclePrefsStore(),
        block: (VehicleViewModel, FakeVehiclePrefsStore) -> Unit,
    ) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody(body))
        try {
            val repo = VehicleRepository(
                TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) },
            )
            val vm = VehicleViewModel(repo, prefs, authOver(FakeSessionStore()))
            awaitCondition(description = "vehicles loaded") { vm.uiState.value is VehiclesUiState.Loaded }
            block(vm, prefs)
        } finally {
            server.shutdown()
        }
    }

    private fun loadedState(vm: VehicleViewModel): VehiclesUiState.Loaded {
        val state = vm.uiState.value
        assertTrue("expected Loaded, was $state", state is VehiclesUiState.Loaded)
        return state as VehiclesUiState.Loaded
    }

    /** Applies pending ViewModel work, then reads the ids currently on screen, in order, across their sections. */
    private fun visibleIds(vm: VehicleViewModel): List<String> {
        dispatcher.scheduler.advanceUntilIdle()
        return loadedState(vm).groups.flatMap { group -> group.tiles.map { it.id } }
    }

    /** Applies pending ViewModel work, then reads each section and the ids under it. */
    private fun sections(vm: VehicleViewModel): List<Pair<BusSection, List<String>>> {
        dispatcher.scheduler.advanceUntilIdle()
        return loadedState(vm).groups.map { group -> group.section to group.tiles.map { it.id } }
    }

    private fun starred(vm: VehicleViewModel): Set<String> =
        loadedState(vm).groups.flatMap { it.tiles }.filter { it.favorite }.map { it.id }.toSet()

    @Test fun `search filters by id and label`() = runTest(dispatcher) {
        withVehicleViewModel { vm, _ ->
            vm.onQueryChange("bus")
            assertEquals(listOf("bus-1", "bus-3"), visibleIds(vm))

            vm.onQueryChange("airport")
            assertEquals(listOf("van-2"), visibleIds(vm))
        }
    }

    @Test fun `search is case insensitive`() = runTest(dispatcher) {
        withVehicleViewModel { vm, _ ->
            vm.onQueryChange("BUS")
            assertEquals(listOf("bus-1", "bus-3"), visibleIds(vm))
        }
    }

    @Test fun `search with an empty query shows all vehicles`() = runTest(dispatcher) {
        withVehicleViewModel { vm, _ ->
            vm.onQueryChange("bus")
            assertEquals(2, visibleIds(vm).size)

            vm.onQueryChange("")
            assertEquals(listOf("van-2", "bus-1", "bus-3"), visibleIds(vm))
        }
    }

    @Test fun `search with no matches empties the list but not the assignments`() = runTest(dispatcher) {
        withVehicleViewModel { vm, _ ->
            vm.onQueryChange("tram")
            assertEquals(emptyList<String>(), visibleIds(vm))
            assertEquals(3, loadedState(vm).vehicles.size)
        }
    }

    @Test fun `autoSelect fires for a single assigned vehicle`() = runTest(dispatcher) {
        withVehicleViewModel(body = """[{"id":"bus-1","label":"Downtown Express"}]""") { vm, _ ->
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals("bus-1", loadedState(vm).autoSelectId)
        }
    }

    @Test fun `autoSelect does not fire when search narrows to one`() = runTest(dispatcher) {
        withVehicleViewModel { vm, _ ->
            vm.onQueryChange("bus-1")

            // The search left exactly one match, but the driver is assigned three vehicles and
            // is still typing — navigating them into a trip here is the bug this test guards.
            assertEquals(listOf("bus-1"), visibleIds(vm))
            assertNull(loadedState(vm).autoSelectId)
        }
    }

    @Test fun `favorites sort first`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore().apply { favoritesState.value = setOf("bus-3") }
        withVehicleViewModel(prefs = prefs) { vm, _ ->
            // "Night Owl" sorts last by label, so only the star can put bus-3 in front.
            assertEquals(listOf("bus-3", "van-2", "bus-1"), visibleIds(vm))
        }
    }

    @Test fun `favorites toggle on and off through the store`() = runTest(dispatcher) {
        withVehicleViewModel { vm, prefs ->
            vm.onToggleFavorite("bus-3")
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals(setOf("bus-3"), prefs.favoritesState.value)
            assertEquals(setOf("bus-3"), starred(vm))
            assertEquals("bus-3", visibleIds(vm).first())

            vm.onToggleFavorite("bus-3")
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals(emptySet<String>(), starred(vm))
            assertEquals(listOf("van-2", "bus-1", "bus-3"), visibleIds(vm))
        }
    }

    @Test fun `recents order most recent first`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore()
        prefs.recordUse("bus-3")
        prefs.recordUse("van-2")
        withVehicleViewModel(prefs = prefs) { vm, _ ->
            // van-2 was used last, so it leads bus-3 even though its label sorts first anyway;
            // bus-1 has never been used and falls to the bottom.
            assertEquals(listOf("van-2", "bus-3", "bus-1"), visibleIds(vm))
        }
    }

    @Test fun `a favorite outranks a more recently used vehicle`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore().apply { favoritesState.value = setOf("bus-3") }
        prefs.recordUse("van-2")
        withVehicleViewModel(prefs = prefs) { vm, _ ->
            assertEquals(listOf("bus-3", "van-2", "bus-1"), visibleIds(vm))
        }
    }

    @Test fun `buses sit under favorites, then recent, then all buses, each in one section`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore().apply { favoritesState.value = setOf("bus-3") }
        // bus-3 is both a favorite and the latest used: it is listed once, as a favorite.
        prefs.recordUse("van-2")
        prefs.recordUse("bus-3")
        withVehicleViewModel(prefs = prefs) { vm, _ ->
            assertEquals(
                listOf(
                    BusSection.FAVORITES to listOf("bus-3"),
                    BusSection.RECENT to listOf("van-2"),
                    BusSection.ALL to listOf("bus-1"),
                ),
                sections(vm),
            )
        }
    }

    @Test fun `a section with no buses is left out`() = runTest(dispatcher) {
        withVehicleViewModel { vm, _ ->
            assertEquals(listOf(BusSection.ALL to listOf("van-2", "bus-1", "bus-3")), sections(vm))
        }
    }

    @Test fun `while searching, every match is under Matches in the usual order`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore().apply { favoritesState.value = setOf("bus-3") }
        withVehicleViewModel(prefs = prefs) { vm, _ ->
            vm.onQueryChange("bus")
            assertEquals(listOf(BusSection.MATCHES to listOf("bus-3", "bus-1")), sections(vm))
        }
    }

    @Test fun `a tile shows the label under the fleet number only when it adds something`() = runTest(dispatcher) {
        val body = """[{"id":"4317","label":"Bus 4317"},{"id":"4318","label":"4318"},{"id":"4319","label":" "}]"""
        withVehicleViewModel(body = body) { vm, _ ->
            dispatcher.scheduler.advanceUntilIdle()
            val labels = loadedState(vm).groups.flatMap { it.tiles }.associate { it.id to it.label }
            assertEquals(mapOf("4317" to "Bus 4317", "4318" to null, "4319" to null), labels)
        }
    }

    @Test fun `ordering is stable across emissions`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore()
        prefs.recordUse("van-2")
        withVehicleViewModel(prefs = prefs) { vm, _ ->
            val before = visibleIds(vm)

            // Two more emissions that land the inputs back exactly where they started.
            vm.onToggleFavorite("bus-1")
            dispatcher.scheduler.advanceUntilIdle()
            vm.onToggleFavorite("bus-1")

            assertEquals(before, visibleIds(vm))
        }
    }
    // --- Route picker: the catalog, searched, with recents pinned ---

    /** Route 15 leads route 5 on purpose: "5" must not match "15". */
    private val catalogRoutesJson =
        """{"routes":[{"id":"R15","short_name":"15","long_name":"Rainier Beach","color":"0077C0","text_color":"FFFFFF","type":3},""" +
            """{"id":"R5","short_name":"5","long_name":"Downtown Loop","color":"","text_color":"","type":3},""" +
            """{"id":"R9","short_name":"9","long_name":"Airport","color":"00AA00","text_color":"000000","type":3}]}"""

    private fun catalogFor(server: MockWebServer) =
        CatalogRepository(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) })

    /**
     * Builds a [RoutesViewModel] over a `MockWebServer` answering `GET /api/v1/gtfs/routes` with
     * [response], waits for the load to settle, and runs [block] against it.
     */
    private fun withRoutesViewModel(
        response: MockResponse = MockResponse().setBody(catalogRoutesJson),
        store: FakeTripStateStore = FakeTripStateStore(),
        block: (RoutesViewModel) -> Unit,
    ) {
        val server = MockWebServer().apply { start() }
        server.enqueue(response)
        try {
            val vm = RoutesViewModel(catalogFor(server), store, authOver(FakeSessionStore()))
            awaitCondition(description = "routes settled") { vm.uiState.value !is RoutesUiState.Loading }
            block(vm)
        } finally {
            server.shutdown()
        }
    }

    private fun loadedRoutes(vm: RoutesViewModel): RoutesUiState.Loaded {
        dispatcher.scheduler.advanceUntilIdle()
        val state = vm.uiState.value
        assertTrue("expected Loaded, was $state", state is RoutesUiState.Loaded)
        return state as RoutesUiState.Loaded
    }

    @Test fun `route search is a prefix on the number and a substring on the name`() = runTest(dispatcher) {
        withRoutesViewModel { vm ->
            vm.onQueryChange("5")
            // Route 15 contains a 5 but does not start with one: a driver typing "5" means route 5.
            assertEquals(listOf("R5"), loadedRoutes(vm).visible.map { it.id })

            vm.onQueryChange("beach")
            assertEquals(listOf("R15"), loadedRoutes(vm).visible.map { it.id })
        }
    }

    @Test fun `route search is case insensitive and ignores surrounding space`() = runTest(dispatcher) {
        withRoutesViewModel { vm ->
            vm.onQueryChange("  BEACH ")
            assertEquals(listOf("R15"), loadedRoutes(vm).visible.map { it.id })
        }
    }

    @Test fun `an empty query shows every route`() = runTest(dispatcher) {
        withRoutesViewModel { vm ->
            assertEquals(listOf("R15", "R5", "R9"), loadedRoutes(vm).visible.map { it.id })
        }
    }

    @Test fun `recent routes are pinned only while the query is empty`() = runTest(dispatcher) {
        val store = FakeTripStateStore()
        store.addRecentRoute("R9")
        withRoutesViewModel(store = store) { vm ->
            assertEquals(listOf("R9"), loadedRoutes(vm).recent.map { it.id })

            vm.onQueryChange("9")
            // Searching is the driver saying which route they want; the shortcut gets out of the way.
            assertEquals(emptyList<String>(), loadedRoutes(vm).recent.map { it.id })
        }
    }

    @Test fun `recent routes keep their recency order and drop ids the catalog no longer has`() = runTest(dispatcher) {
        val store = FakeTripStateStore()
        store.addRecentRoute("R5")
        store.addRecentRoute("RETIRED")
        store.addRecentRoute("R9")
        withRoutesViewModel(store = store) { vm ->
            // Newest first, and the route the agency has since dropped from the feed is skipped
            // rather than shown as a row that cannot be tapped.
            assertEquals(listOf("R9", "R5"), loadedRoutes(vm).recent.map { it.id })
        }
    }

    private fun routeItem(id: String, longName: String) = RouteItem(id, RouteBadgeModel(id, null, null), longName)

    @Test fun `routes are a grid only when the feed names none of them`() {
        assertTrue(showsAsGrid(listOf(routeItem("7", ""), routeItem("8", " "))))
        // One named route keeps every route a row, so the list never mixes rows and badges.
        assertFalse(showsAsGrid(listOf(routeItem("7", ""), routeItem("8", "Rainier Beach"))))
        assertFalse(showsAsGrid(listOf(routeItem("7", "Downtown"), routeItem("8", "Rainier Beach"))))
    }

    @Test fun `a search does not change rows into a grid`() = runTest(dispatcher) {
        val mixed = """{"routes":[{"id":"R7","short_name":"7","long_name":"","color":"","text_color":"","type":3},""" +
            """{"id":"R8","short_name":"8","long_name":"Rainier Beach","color":"","text_color":"","type":3}]}"""
        withRoutesViewModel(response = MockResponse().setBody(mixed)) { vm ->
            assertFalse(loadedRoutes(vm).grid)

            // Only the unnamed route is left, but the layout follows the whole feed.
            vm.onQueryChange("7")
            assertEquals(listOf("R7"), loadedRoutes(vm).visible.map { it.id })
            assertFalse(loadedRoutes(vm).grid)
        }
    }

    @Test fun `a failed route load offers a retry, and a 401 ends the session`() = runTest(dispatcher) {
        withRoutesViewModel(response = MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START)) { vm ->
            assertEquals(RoutesUiState.Error, vm.uiState.value)
        }
        withRoutesViewModel(response = MockResponse().setResponseCode(401).setBody("""{"error":"bad token"}""")) { vm ->
            // Retrying with the same dead token only fails again; the driver has to sign in.
            assertEquals(RoutesUiState.SessionEnded, vm.uiState.value)
        }
    }

    @Test fun `a server with no schedule has no routes to offer and no retry`() = runTest(dispatcher) {
        // net/http's own 404: GTFS_STATIC_URL is unset, so the catalog was never registered.
        val unregistered = MockResponse().setResponseCode(404)
            .setHeader("Content-Type", "text/plain; charset=utf-8")
            .setBody("404 page not found\n")
        withRoutesViewModel(response = unregistered) { vm ->
            assertEquals(RoutesUiState.NoSchedule, vm.uiState.value)
        }
    }

    @Test fun `a schedule that is still loading reads the same as none`() = runTest(dispatcher) {
        val loading = MockResponse().setResponseCode(503).setBody("""{"error":"schedule data unavailable"}""")
        withRoutesViewModel(response = loading) { vm ->
            // Both mean "nothing to pick from"; neither is a failure the driver can act on.
            assertEquals(RoutesUiState.NoSchedule, vm.uiState.value)
        }
    }

    // --- Run picker: today's runs, the current one badged, and the start it performs ---

    /** Two runs of fixture route R1: 08:00-08:10 and 10:00-10:10, America/Los_Angeles. */
    private val twoRunsJson =
        """{"route_id":"R1","service_date":"20260922","timezone":"America/Los_Angeles","trips":[""" +
            """{"id":"T1","headsign":"North","direction_id":0,"starts_at":"2026-09-22T08:00:00-07:00",""" +
            """"ends_at":"2026-09-22T08:10:00-07:00","first_stop":"Stop ST1","last_stop":"Stop ST3"},""" +
            """{"id":"T4","headsign":"North","direction_id":null,"starts_at":"2026-09-22T10:00:00-07:00",""" +
            """"ends_at":"2026-09-22T10:10:00-07:00","first_stop":"Stop ST1","last_stop":"Stop ST3"}]}"""

    /** T1's geometry, dated the service day [twoRunsJson] lists it under. */
    private val t1GeometryJson = TripFixtures.tripJson(serviceDate = "20260922")

    /**
     * Builds a [RunsViewModel] over a `MockWebServer` answering the trips call with [tripsBody],
     * every geometry fetch with [geometry], each start with the next of [startResponses], and
     * every end with [endResponse], loads route R1 at wall-clock time [now] (or on [wallClock], for a test that moves
     * the time on), and runs [block] against it.
     */
    private fun withRunsViewModel(
        tripsBody: String = twoRunsJson,
        now: String = "2026-09-22T08:05:00-07:00",
        geometry: () -> MockResponse = { MockResponse().setBody(t1GeometryJson) },
        startResponses: List<MockResponse> = emptyList(),
        endResponse: () -> MockResponse = { MockResponse().setBody("""{"status":"trip ended"}""") },
        geometryStore: TripGeometryStore = FakeTripGeometryStore(),
        extraArgs: Map<String, String> = emptyMap(),
        wallClock: (() -> Long)? = null,
        sessionStore: SessionStore = FakeSessionStore(),
        block: (RunsViewModel, MockWebServer, FakeServiceController, FakeTripStateStore) -> Unit,
    ) {
        val server = MockWebServer().apply { start() }
        val starts = ConcurrentLinkedQueue(startResponses)
        // Routed by path rather than queued: every start fetches the run's geometry before it
        // posts, and a reload fetches the list again.
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when {
                request.path == "/api/v1/trips/start" -> starts.poll() ?: MockResponse().setResponseCode(500)
                request.path == "/api/v1/trips/end" -> endResponse()
                request.path.orEmpty().startsWith("/api/v1/gtfs/trips/") -> geometry()
                else -> MockResponse().setBody(tripsBody)
            }
        }
        try {
            val provider = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
            val nowEpochSec = OffsetDateTime.parse(now).toEpochSecond()
            val clock = wallClock ?: { nowEpochSec }
            val tripState = FakeTripStateStore()
            val tripRepository = TripRepository(provider, tripState, FakeVehiclePrefsStore(), sessionStore, clock = clock)
            val serviceController = FakeServiceController()
            // The nav graph's arguments, which is where the ViewModel reads them from — and it
            // loads off them in init, with no load() call from the screen.
            val args = SavedStateHandle(mapOf("vehicleId" to "bus-1", "routeId" to "R1") + extraArgs)
            val vm = RunsViewModel(
                args, CatalogRepository(provider), tripRepository, geometryStore, serviceController, authOver(sessionStore), clock,
            )
            awaitCondition(description = "runs settled") { vm.uiState.value !is RunsUiState.Loading }
            block(vm, server, serviceController, tripState)
        } finally {
            server.shutdown()
        }
    }

    private fun loadedRuns(vm: RunsViewModel): RunsUiState.Loaded {
        dispatcher.scheduler.advanceUntilIdle()
        val state = vm.uiState.value
        assertTrue("expected Loaded, was $state", state is RunsUiState.Loaded)
        return state as RunsUiState.Loaded
    }

    @Test fun `the run under way is badged Now`() = runTest(dispatcher) {
        withRunsViewModel(now = "2026-09-22T08:05:00-07:00") { vm, _, _, _ ->
            val state = loadedRuns(vm)
            val runs = state.boards.flatMap { it.runs }.associateBy { it.id }
            assertEquals(RunHighlight.NOW, runs.getValue("T1").highlight)
            assertNull(runs.getValue("T4").highlight)
            // The zone is the agency's, read off the response rather than off the device.
            assertEquals("America/Los_Angeles", state.page.timezone.id)
        }
    }

    @Test fun `the next run to start is badged Next once the earlier one is over`() = runTest(dispatcher) {
        withRunsViewModel(now = "2026-09-22T09:00:00-07:00") { vm, _, _, _ ->
            val runs = loadedRuns(vm).boards.flatMap { it.runs }.associateBy { it.id }
            assertEquals(RunHighlight.NEXT, runs.getValue("T4").highlight)
            assertTrue("the run before it has gone", runs.getValue("T1").past)
        }
    }

    @Test fun `while the step is on screen the boards move on at each new minute`() = runTest(dispatcher) {
        var nowEpochSec = OffsetDateTime.parse("2026-09-22T08:05:30-07:00").toEpochSecond()
        withRunsViewModel(wallClock = { nowEpochSec }) { vm, _, _, _ ->
            fun runs() = (vm.uiState.value as RunsUiState.Loaded).boards.flatMap { it.runs }.associateBy { it.id }
            assertEquals(RunHighlight.NOW, loadedRuns(vm).boards.flatMap { it.runs }.single { it.id == "T1" }.highlight)

            vm.onScreenVisible(true)
            // Stopped whatever happens: a ticker left running would keep later tests' advanceUntilIdle from ending.
            try {
                dispatcher.scheduler.runCurrent()
                // T1 is over by 09:00, but the board waits for the minute to turn rather than rebuilding all the time.
                nowEpochSec = OffsetDateTime.parse("2026-09-22T09:00:00-07:00").toEpochSecond()
                dispatcher.scheduler.advanceTimeBy(29_999)
                dispatcher.scheduler.runCurrent()
                assertEquals(RunHighlight.NOW, runs().getValue("T1").highlight)

                dispatcher.scheduler.advanceTimeBy(1)
                dispatcher.scheduler.runCurrent()
                assertTrue("T1 has gone", runs().getValue("T1").past)
                assertEquals(RunHighlight.NEXT, runs().getValue("T4").highlight)
            } finally {
                vm.onScreenVisible(false)
            }
        }
    }

    @Test fun `off screen the boards stay put, and catch up as soon as the step is back`() = runTest(dispatcher) {
        var nowEpochSec = OffsetDateTime.parse("2026-09-22T08:05:30-07:00").toEpochSecond()
        withRunsViewModel(wallClock = { nowEpochSec }) { vm, _, _, _ ->
            fun t1() = (vm.uiState.value as RunsUiState.Loaded).boards.flatMap { it.runs }.single { it.id == "T1" }
            try {
                vm.onScreenVisible(true)
                dispatcher.scheduler.runCurrent()
                vm.onScreenVisible(false)

                nowEpochSec = OffsetDateTime.parse("2026-09-22T09:00:00-07:00").toEpochSecond()
                dispatcher.scheduler.advanceTimeBy(10 * 60_000)
                dispatcher.scheduler.runCurrent()
                assertEquals(RunHighlight.NOW, t1().highlight)

                vm.onScreenVisible(true)
                dispatcher.scheduler.runCurrent()
                assertTrue("T1 has gone", t1().past)
            } finally {
                vm.onScreenVisible(false)
            }
        }
    }

    @Test fun `a route with no runs today loads empty rather than failing`() = runTest(dispatcher) {
        val body = """{"route_id":"R1","service_date":"20260922","timezone":"America/Los_Angeles","trips":[]}"""
        withRunsViewModel(tripsBody = body) { vm, _, _, _ ->
            // A schedule exists, this route just is not running today — not a reason to doubt
            // the catalog.
            val state = loadedRuns(vm)
            assertEquals(emptyList<String>(), state.page.runs.map { it.id })
            assertEquals(emptyList<BoardRun>(), state.boards.single().runs)
        }
    }

    @Test fun `a route's runs are loaded once, off the nav arguments`() = runTest(dispatcher) {
        withRunsViewModel { vm, server, _, _ ->
            // The ViewModel reads routeId from its SavedStateHandle and loads in init, so the
            // screen has no load() to re-run — which is what made every rotation refetch.
            assertEquals("/api/v1/gtfs/routes/R1/trips", server.takeRequest().path)
            assertEquals(1, server.requestCount)
            assertEquals(listOf("T1", "T4"), loadedRuns(vm).page.runs.map { it.id })
        }
    }

    @Test fun `the route's badge comes from the nav arguments, read once`() = runTest(dispatcher) {
        val routeArgs = mapOf("routeName" to "E Line", "routeColor" to "9C182F", "routeTextColor" to "FFFFFF")
        withRunsViewModel(extraArgs = routeArgs) { vm, _, _, _ ->
            assertEquals(RouteBadgeModel("E Line", Color(0xFF9C182F), Color(0xFFFFFFFF)), vm.route)
            assertEquals("bus-1", vm.vehicleId)
        }
    }

    @Test fun `starting an after-midnight run saves the catalog's service date`() = runTest(dispatcher) {
        // The fixture's 25:00 case: a run that departs at 01:00 still belongs to the service
        // date before it, so the device's calendar date would put the trip a day late.
        val afterMidnight =
            """{"route_id":"R2","service_date":"20260921","timezone":"America/Los_Angeles","trips":[""" +
                """{"id":"T3","headsign":"Loop","direction_id":1,"starts_at":"2026-09-22T01:00:00-07:00",""" +
                """"ends_at":"2026-09-22T01:20:00-07:00","first_stop":"Stop LP1","last_stop":"Stop LP1"}]}"""
        val started = MockResponse().setResponseCode(201).setBody(
            """{"id":9,"user_id":1,"vehicle_id":"bus-1","route_id":"R2","gtfs_trip_id":"T3","start_time":"2026-09-22T08:00:00Z","status":"active"}""",
        )
        withRunsViewModel(
            tripsBody = afterMidnight,
            now = "2026-09-22T01:05:00-07:00",
            geometry = { MockResponse().setBody(TripFixtures.tripJson(id = "T3", serviceDate = "20260921")) },
            startResponses = listOf(started),
        ) { vm, _, _, tripState ->
            var navigated = false
            vm.onStartRun("T3") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            assertEquals("20260921", tripState.tripState.value!!.startDate)
        }
    }

    @Test fun `starting a run sends the catalog's route id and trip id, then starts tracking`() = runTest(dispatcher) {
        val started = MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}""",
        )
        withRunsViewModel(startResponses = listOf(started)) { vm, server, serviceController, _ ->
            var navigated = false
            vm.onStartRun("T1") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            assertEquals(1, serviceController.startCount)
            server.takeRequest() // the trips call
            server.takeRequest() // the run's geometry
            val body = Json.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
            // The whole point of the picker: real ids from the schedule, not typed ones.
            assertEquals("R1", body["route_id"]!!.jsonPrimitive.content)
            assertEquals("T1", body["gtfs_trip_id"]!!.jsonPrimitive.content)
        }
    }

    @Test fun `starting a run maps 403 to NOT_ASSIGNED and 409 to TRIP_ACTIVE`() = runTest(dispatcher) {
        val refusals = listOf(
            MockResponse().setResponseCode(403).setBody("""{"error":"driver is not assigned to this vehicle"}"""),
            MockResponse().setResponseCode(409).setBody("""{"error":"driver already has an active trip"}"""),
        )
        val store = FakeSessionStore().apply { state.value = signedIn }
        withRunsViewModel(startResponses = refusals, sessionStore = store) { vm, _, serviceController, _ ->
            vm.onStartRun("T1") { }
            awaitCondition(description = "403 surfaced") { loadedRuns(vm).error != null }
            assertEquals(TripError.NOT_ASSIGNED, loadedRuns(vm).error)

            vm.onStartRun("T1") { }
            awaitCondition(description = "409 surfaced") { loadedRuns(vm).error == TripError.TRIP_ACTIVE }
            assertEquals(0, serviceController.startCount)
            assertEquals("a refusal is not the end of the session", signedIn, store.state.value)
        }
    }

    @Test fun `a start the network or the server fails keeps the session and offers Try again`() = runTest(dispatcher) {
        val failures = listOf(
            MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST) to TripError.NETWORK,
            MockResponse().setResponseCode(500).setBody("""{"error":"failed to start trip"}""") to TripError.OTHER,
        )
        for ((failure, error) in failures) {
            val store = FakeSessionStore().apply { state.value = signedIn }
            withRunsViewModel(startResponses = listOf(failure), sessionStore = store) { vm, _, _, _ ->
                vm.onStartRun("T1") { fail("a start that failed must not leave the screen") }
                awaitCondition(description = "$error surfaced") { loadedRuns(vm).error != null }

                assertEquals(error, loadedRuns(vm).error)
                assertEquals(signedIn, store.state.value)
            }
        }
    }

    @Test fun `a geometry fetch that gets a 401 ends the session and starts nothing`() = runTest(dispatcher) {
        val store = FakeSessionStore().apply { state.value = signedIn }
        withRunsViewModel(geometry = ::unauthorized, sessionStore = store) { vm, server, serviceController, tripState ->
            vm.onStartRun("T1") { fail("nothing may start once the server has ended the session") }
            awaitCondition(description = "session ended") { vm.uiState.value == RunsUiState.SessionEnded }

            assertEquals(Session(signedIn.serverUrl, null, null, email = signedIn.email), store.state.value)
            assertTrue("/api/v1/trips/start" !in requestPaths(server))
            assertEquals(0, serviceController.startCount)
            assertNull(tripState.tripState.value)
        }
    }

    @Test fun `a start that gets a 401 ends the session`() = runTest(dispatcher) {
        val store = FakeSessionStore().apply { state.value = signedIn }
        withRunsViewModel(startResponses = listOf(unauthorized()), sessionStore = store) { vm, server, serviceController, tripState ->
            vm.onStartRun("T1") { fail("nothing may start once the server has ended the session") }
            awaitCondition(description = "session ended") { vm.uiState.value == RunsUiState.SessionEnded }

            assertEquals(Session(signedIn.serverUrl, null, null, email = signedIn.email), store.state.value)
            assertTrue("the start is what got the 401", "/api/v1/trips/start" in requestPaths(server))
            assertEquals(0, serviceController.startCount)
            assertNull(tripState.tripState.value)
        }
    }

    @Test fun `a leftover end that gets a 401 ends the session, and keeps the end`() = runTest(dispatcher) {
        val store = FakeSessionStore().apply { state.value = signedIn }
        withRunsViewModel(endResponse = ::unauthorized, sessionStore = store) { vm, server, serviceController, tripState ->
            tripState.unconfirmedEndsState.value = listOf(UnconfirmedEnd(7L, null))
            vm.onStartRun("T1") { fail("nothing may start while the previous trip is open") }
            awaitCondition(description = "session ended") { vm.uiState.value == RunsUiState.SessionEnded }

            assertEquals(Session(signedIn.serverUrl, null, null, email = signedIn.email), store.state.value)
            assertEquals("kept for this driver's next sign-in", listOf(UnconfirmedEnd(7L, null)), tripState.unconfirmedEndsState.value)
            assertTrue("/api/v1/trips/start" !in requestPaths(server))
            assertEquals(0, serviceController.startCount)
        }
    }

    @Test fun `a leftover end that fails is reported as the previous trip, not this one`() = runTest(dispatcher) {
        val refused = { MockResponse().setResponseCode(500).setBody("""{"error":"failed to end trip"}""") }
        withRunsViewModel(endResponse = refused) { vm, server, serviceController, tripState ->
            tripState.unconfirmedEndsState.value = listOf(UnconfirmedEnd(7L, null))
            vm.onStartRun("T1") { fail("nothing may start while the previous trip is open") }
            awaitCondition(description = "start failure surfaced") { loadedRuns(vm).error != null }

            assertEquals(TripError.PREVIOUS_TRIP_NOT_ENDED, loadedRuns(vm).error)
            assertEquals(0, serviceController.startCount)
        }
    }

    @Test fun `a second tap while a start is in flight is ignored`() = runTest(dispatcher) {
        val started = MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}""",
        )
        withRunsViewModel(startResponses = listOf(started)) { vm, server, _, _ ->
            var navigated = false
            vm.onStartRun("T1") { navigated = true }
            // The rows are disabled while a start is in flight, but a tap already on its way in
            // must not turn into a second POST — the server would answer that one 409.
            vm.onStartRun("T4") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            // The list, then one geometry fetch and one POST: T1's alone.
            assertEquals(3, server.requestCount)
        }
    }

    // A sign-out under a start would leave the trip started with nobody signed in to end it.
    @Test fun `the account menu waits while a start is in flight`() = runTest(dispatcher) {
        // Holds the run's geometry back, which the start waits on before it posts.
        val held = CountDownLatch(1)
        withRunsViewModel(geometry = { held.await(5, TimeUnit.SECONDS); MockResponse().setBody(t1GeometryJson) }) { vm, _, _, _ ->
            assertTrue("before the tap", canSignOut(vm.uiState.value))

            vm.onStartRun("T1") {}
            dispatcher.scheduler.runCurrent()
            assertFalse("while the start is out", canSignOut(vm.uiState.value))

            held.countDown()
            awaitCondition(description = "the start settled") { vm.uiState.value.let { it is RunsUiState.Loaded && !it.starting } }
            assertTrue("once it has settled", canSignOut(vm.uiState.value))
        }
        assertTrue("the other states", listOf(RunsUiState.Loading, RunsUiState.Error, RunsUiState.SessionEnded).all(::canSignOut))
    }

    // --- Starting a run: the geometry is fetched and checked before the server is told ---

    private fun requestPaths(server: MockWebServer): List<String> =
        List(server.requestCount) { server.takeRequest().path.orEmpty() }

    @Test fun `the run's geometry is fetched before the trip is started`() = runTest(dispatcher) {
        val started = MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}""",
        )
        withRunsViewModel(startResponses = listOf(started)) { vm, server, _, _ ->
            var navigated = false
            vm.onStartRun("T1") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            assertEquals(
                listOf("/api/v1/gtfs/routes/R1/trips", "/api/v1/gtfs/trips/T1", "/api/v1/trips/start"),
                requestPaths(server),
            )
        }
    }

    @Test fun `a run with too short a shape is refused before the server is told`() = runTest(dispatcher) {
        val onePoint = TripFixtures.tripJson(serviceDate = "20260922", points = "[[47.6,-122.33]]")
        val geometryStore = FakeTripGeometryStore()
        withRunsViewModel(
            geometry = { MockResponse().setBody(onePoint) },
            geometryStore = geometryStore,
        ) { vm, server, serviceController, tripState ->
            vm.onStartRun("T1") { fail("a run with nothing to judge against must not start") }
            awaitCondition(description = "refusal surfaced") { loadedRuns(vm).error != null }

            assertEquals(TripError.NO_GEOMETRY, loadedRuns(vm).error)
            assertTrue("/api/v1/trips/start" !in requestPaths(server))
            assertEquals(0, serviceController.startCount)
            assertNull(tripState.tripState.value)
            assertNull(geometryStore.stored)
        }
    }

    @Test fun `a run with no stops is refused before the server is told`() = runTest(dispatcher) {
        val noStops = TripFixtures.tripJson(serviceDate = "20260922", stops = "[]")
        withRunsViewModel(geometry = { MockResponse().setBody(noStops) }) { vm, server, _, _ ->
            vm.onStartRun("T1") { fail("a run with nothing to judge against must not start") }
            awaitCondition(description = "refusal surfaced") { loadedRuns(vm).error != null }

            assertEquals(TripError.NO_GEOMETRY, loadedRuns(vm).error)
            assertTrue("/api/v1/trips/start" !in requestPaths(server))
        }
    }

    @Test fun `a run gone out of service is refused, and a reload clears the refusal`() = runTest(dispatcher) {
        val notActive = MockResponse().setResponseCode(422).setBody("""{"error":"trip not active on date"}""")
        withRunsViewModel(geometry = { notActive }) { vm, server, _, _ ->
            vm.onStartRun("T1") { fail("a run not in service must not start") }
            awaitCondition(description = "refusal surfaced") { loadedRuns(vm).error != null }
            assertEquals(TripError.TRIP_NOT_ACTIVE, loadedRuns(vm).error)

            vm.retry()
            awaitCondition(description = "runs reloaded") { vm.uiState.value is RunsUiState.Loaded }

            assertNull(loadedRuns(vm).error)
            assertEquals(
                listOf("/api/v1/gtfs/routes/R1/trips", "/api/v1/gtfs/trips/T1", "/api/v1/gtfs/routes/R1/trips"),
                requestPaths(server),
            )
        }
    }

    @Test fun `a run the server now dates to another service day is refused`() = runTest(dispatcher) {
        // The list was loaded for the 22nd; past 03:00 on the 23rd the server resolves the same
        // run to the 23rd, which the list's service date would then misreport in the feed.
        val nextDay = TripFixtures.tripJson(serviceDate = "20260923")
        withRunsViewModel(geometry = { MockResponse().setBody(nextDay) }) { vm, server, _, _ ->
            vm.onStartRun("T1") { fail("a run dated to another day must not start") }
            awaitCondition(description = "refusal surfaced") { loadedRuns(vm).error != null }

            assertEquals(TripError.TRIP_NOT_ACTIVE, loadedRuns(vm).error)
            assertTrue("/api/v1/trips/start" !in requestPaths(server))
        }
    }

    private val t1Started = MockResponse().setResponseCode(201).setBody(
        """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}""",
    )

    @Test fun `a started run's geometry is saved for the tracking service`() = runTest(dispatcher) {
        val geometryStore = FakeTripGeometryStore()
        withRunsViewModel(startResponses = listOf(t1Started), geometryStore = geometryStore) { vm, _, _, _ ->
            var navigated = false
            vm.onStartRun("T1") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            val saved = requireNotNull(geometryStore.stored)
            assertEquals("T1", saved.tripId)
            assertEquals("20260922", saved.serviceDate)
            assertEquals(3, saved.shapePoints.size)
        }
    }

    @Test fun `a geometry save that fails still completes the start`() = runTest(dispatcher) {
        // The trip is running on the server by the time the save is reached, so a disk error
        // there must not be reported as a failed start — the driver's retry would come back 409.
        val failingStore = object : TripGeometryStore {
            override suspend fun save(geometry: TripGeometry): Unit = throw IOException("disk full")
            override suspend fun load(): TripGeometry? = null
            override suspend fun clear() = Unit
        }
        withRunsViewModel(startResponses = listOf(t1Started), geometryStore = failingStore) { vm, _, serviceController, tripState ->
            var navigated = false
            vm.onStartRun("T1") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            assertEquals(1, serviceController.startCount)
            assertEquals("T1", tripState.tripState.value?.gtfsTripId)
            assertNull(loadedRuns(vm).error)
        }
    }

    // --- A session the server has ended: the same on all three pickers ---

    private val signedIn = Session("https://tracker.example.com/", "a1", 100L, "r1", 1_000L, "d@example.com")

    private fun unauthorized() = MockResponse().setResponseCode(401).setBody("""{"error":"invalid or expired token"}""")

    /** A picker, whichever it is, in terms of its own states. [goodReply] is a body that loads it. */
    private class Picker(
        val name: String,
        val goodReply: String,
        val state: StateFlow<Any>,
        val loading: Any,
        val error: Any,
        val sessionEnded: Any,
        val isLoaded: () -> Boolean,
        val retry: () -> Unit,
    )

    private fun busStep(provider: TrackerApiProvider, store: SessionStore): Picker {
        val vm = VehicleViewModel(VehicleRepository(provider), FakeVehiclePrefsStore(), authOver(store))
        return Picker(
            "bus step", threeVehiclesJson, vm.uiState, VehiclesUiState.Loading, VehiclesUiState.Error, VehiclesUiState.SessionEnded,
            { vm.uiState.value is VehiclesUiState.Loaded }, vm::retry,
        )
    }

    private fun routeStep(provider: TrackerApiProvider, store: SessionStore): Picker {
        val vm = RoutesViewModel(CatalogRepository(provider), FakeTripStateStore(), authOver(store))
        return Picker(
            "route step", catalogRoutesJson, vm.uiState, RoutesUiState.Loading, RoutesUiState.Error, RoutesUiState.SessionEnded,
            { vm.uiState.value is RoutesUiState.Loaded }, vm::retry,
        )
    }

    private fun runStep(provider: TrackerApiProvider, store: SessionStore): Picker {
        val clock = { OffsetDateTime.parse("2026-09-22T08:05:00-07:00").toEpochSecond() }
        val args = SavedStateHandle(mapOf("vehicleId" to "bus-1", "routeId" to "R1"))
        val trips = TripRepository(provider, FakeTripStateStore(), FakeVehiclePrefsStore(), store, clock = clock)
        val vm = RunsViewModel(args, CatalogRepository(provider), trips, FakeTripGeometryStore(), FakeServiceController(), authOver(store), clock)
        return Picker(
            "run step", twoRunsJson, vm.uiState, RunsUiState.Loading, RunsUiState.Error, RunsUiState.SessionEnded,
            { vm.uiState.value is RunsUiState.Loaded }, vm::retry,
        )
    }

    /**
     * Runs [block] on each picker, with a store holding [signedIn] and a server answering the first load with
     * [firstReply] and the next with one that works. The first load is still to come when [block] starts: the pickers
     * load through the test dispatcher.
     */
    private fun onEachPicker(
        firstReply: () -> MockResponse,
        storeFor: (FakeSessionStore) -> SessionStore = { it },
        block: (Picker, FakeSessionStore) -> Unit,
    ) {
        for (build in listOf(::busStep, ::routeStep, ::runStep)) {
            val store = FakeSessionStore().apply { state.value = signedIn }
            val server = MockWebServer().apply { start() }
            try {
                val provider = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
                val picker = build(provider, storeFor(store))
                server.enqueue(firstReply())
                server.enqueue(MockResponse().setBody(picker.goodReply))
                block(picker, store)
            } finally {
                server.shutdown()
            }
        }
    }

    private fun Picker.awaitFirstLoad() = awaitCondition(description = "$name settled") { state.value != loading }

    @Test fun `a 401 ends the session and keeps the server and the email`() = runTest(dispatcher) {
        onEachPicker(firstReply = ::unauthorized) { picker, store ->
            picker.awaitFirstLoad()

            assertEquals(picker.name, picker.sessionEnded, picker.state.value)
            assertEquals(picker.name, Session(signedIn.serverUrl, null, null, email = signedIn.email), store.state.value)
        }
    }

    @Test fun `a failed load keeps the session and offers a retry`() = runTest(dispatcher) {
        val failures = listOf(
            { MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START) },
            { MockResponse().setResponseCode(500).setBody("""{"error":"internal server error"}""") },
        )
        for (failure in failures) {
            onEachPicker(firstReply = failure) { picker, store ->
                picker.awaitFirstLoad()

                assertEquals(picker.name, picker.error, picker.state.value)
                assertEquals(picker.name, signedIn, store.state.value)

                picker.retry()
                awaitCondition(description = "${picker.name} loaded on the retry") { picker.isLoaded() }

                assertEquals(picker.name, signedIn, store.state.value)
            }
        }
    }

    // A write takes time on a real disk. A screen that heard first would open Sign in with the dead token still stored.
    @Test fun `the session is cleared before the screen hears it ended`() = runTest(dispatcher) {
        val slowToClear = { store: FakeSessionStore ->
            object : SessionStore by store {
                override suspend fun clearToken() {
                    delay(1)
                    store.clearToken()
                }
            }
        }
        onEachPicker(firstReply = ::unauthorized, storeFor = slowToClear) { picker, store ->
            var tokenWhenEnded: String? = "the screen never heard"
            val screen = launch {
                picker.state.first { it == picker.sessionEnded }
                tokenWhenEnded = store.state.value.token
            }
            awaitCondition(description = "${picker.name} heard the session ended") { screen.isCompleted }

            assertNull(picker.name, tokenWhenEnded)
        }
    }

    @Test fun `a session that can't be cleared still goes to Sign in`() = runTest(dispatcher) {
        val fullDisk = { store: FakeSessionStore -> store.apply { clearFailure = IOException("No space left on device") } }
        onEachPicker(firstReply = ::unauthorized, storeFor = fullDisk) { picker, store ->
            picker.awaitFirstLoad()

            assertEquals(picker.name, picker.sessionEnded, picker.state.value)
            assertEquals("still stored, so the next 401 ends it again", signedIn, store.state.value)
        }
    }

    @Test fun `the next launch after a 401 opens Sign in`() = runTest(dispatcher) {
        onEachPicker(firstReply = ::unauthorized) { picker, store ->
            picker.awaitFirstLoad()

            val nextLaunch = appNavViewModel(sessionStore = store)
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(picker.name, "login", nextLaunch.startDestination.value)
        }
    }

    // --- Sign out, from the account menu on the pickers ---

    private val driver2 = jwtFor("2")

    private fun ended() = MockResponse().setBody("""{"status":"trip ended"}""")
    private fun loggedOut() = MockResponse().setResponseCode(204)

    /**
     * Like [awaitCondition], but the test clock moves on no faster than real time. Skipping ahead, as advanceUntilIdle
     * does, would run out [SIGN_OUT_WAIT_MS] at once, while the requests are still on their way to the server.
     */
    private fun awaitInRealTime(description: String, condition: () -> Boolean) {
        val deadlineNanos = System.nanoTime() + 5_000_000_000
        while (!condition() && System.nanoTime() < deadlineNanos) {
            dispatcher.scheduler.advanceTimeBy(1)
            dispatcher.scheduler.runCurrent()
            Thread.sleep(1)
        }
        if (!condition()) fail("Timed out after 5000ms waiting for: $description")
    }

    /** The account menu's view model, signed in as driver 2, over a [MockWebServer] that answers in the order enqueued. */
    private inner class SignOut(
        val trips: FakeTripStateStore = FakeTripStateStore(),
        val prefs: FakeVehiclePrefsStore = FakeVehiclePrefsStore(),
        storeFor: (FakeSessionStore) -> SessionStore = { it },
    ) : AutoCloseable {
        val server = MockWebServer().apply { start() }
        val serverUrl = server.url("/").toString()
        val signedIn = Session(serverUrl, driver2, 100L, "r2", 1_000L, "d@example.com")
        val sessions = FakeSessionStore().apply { state.value = signedIn }
        val store = storeFor(sessions)

        // Sends the token the store holds when each request goes, as the app's client does.
        private val provider = TrackerApiProvider { ApiFactory { sessions.state.value.token }.create(serverUrl) }
        val vm = AccountViewModel(store, authOver(store, provider), TripRepository(provider, trips, prefs, store, clock = { 0L }))

        /** Every request the server got, in order. Call once, after the requests are done. */
        fun requests(): List<RecordedRequest> = List(server.requestCount) { server.takeRequest() }

        fun signOut() {
            vm.onSignOut()
            awaitInRealTime("signed out") { vm.uiState.value.signedOut }
        }

        override fun close() = server.shutdown()
    }

    @Test fun `the menu shows who is signed in, and the server as its host`() = runTest(dispatcher) {
        val sessions = FakeSessionStore().apply { state.value = Session("https://tracker.example.com/", "a1", 100L, email = "d@example.com") }
        val vm = AccountViewModel(sessions, authOver(sessions), TripRepository({ error("no server") }, FakeTripStateStore(), FakeVehiclePrefsStore(), sessions) { 0L })
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(AccountUiState(email = "d@example.com", server = "tracker.example.com"), vm.uiState.value)
    }

    @Test fun `the version reads as its name and build, with a mark for a build with no name`() {
        assertEquals("1.2.3 (45)", versionText("1.2.3", 45))
        assertEquals("? (45)", versionText(null, 45))
    }

    @Test fun `signing out sends the ends, then logs out, then forgets the driver`() = runTest(dispatcher) {
        SignOut(trips = FakeTripStateStore().apply { unconfirmedEndsState.value = listOf(UnconfirmedEnd(7L, "2")) }).use { s ->
            s.server.enqueue(ended())
            s.server.enqueue(loggedOut())

            s.signOut()

            assertEquals(listOf("/api/v1/trips/end", "/api/v1/auth/logout"), s.requests().map { it.path })
            assertEquals("the server stays, and nothing else", Session(s.serverUrl, null, null), s.sessions.state.value)
            assertEquals(emptyList<UnconfirmedEnd>(), s.trips.unconfirmedEnds.first())
        }
    }

    /** Suspends before it clears, as a DataStore write does: a write cancelled on the way never happens. */
    private val slowToClear = { store: FakeSessionStore ->
        object : SessionStore by store {
            override suspend fun clearTokenAndEmail() {
                delay(1)
                store.clearTokenAndEmail()
            }
        }
    }

    // A screen that heard first would open Sign in with the old driver's token still stored.
    @Test fun `the token is still there for the logout, and gone before Sign in opens`() = runTest(dispatcher) {
        SignOut(storeFor = slowToClear).use { s ->
            s.server.enqueue(loggedOut())
            var tokenWhenSignedOut: String? = "the screen never heard"
            val screen = launch {
                s.vm.uiState.first { it.signedOut }
                tokenWhenSignedOut = s.sessions.state.value.token
            }

            s.vm.onSignOut()
            awaitInRealTime("the screen heard") { screen.isCompleted }

            assertEquals("Bearer $driver2", s.requests().single().getHeader("Authorization"))
            assertNull(tokenWhenSignedOut)
        }
    }

    @Test fun `a failed end doesn't stop the sign-out`() = runTest(dispatcher) {
        SignOut(trips = FakeTripStateStore().apply { unconfirmedEndsState.value = listOf(UnconfirmedEnd(7L, "2")) }).use { s ->
            s.server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"failed to end trip"}"""))
            s.server.enqueue(loggedOut())

            s.signOut()

            assertEquals(listOf("/api/v1/trips/end", "/api/v1/auth/logout"), s.requests().map { it.path })
            assertEquals(Session(s.serverUrl, null, null), s.sessions.state.value)
            assertEquals("kept for driver 2's next start", listOf(UnconfirmedEnd(7L, "2")), s.trips.unconfirmedEnds.first())
        }
    }

    @Test fun `a failed logout still signs out on this phone`() = runTest(dispatcher) {
        val failures = mapOf(
            "500" to MockResponse().setResponseCode(500).setBody("""{"error":"internal server error"}"""),
            "401" to MockResponse().setResponseCode(401).setBody("""{"error":"invalid or expired token"}"""),
            "no connection" to MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START),
        )
        for ((name, failure) in failures) {
            SignOut().use { s ->
                s.server.enqueue(failure)

                s.signOut()

                assertEquals(name, Session(s.serverUrl, null, null), s.sessions.state.value)
            }
        }
    }

    // Out of coverage OkHttp would wait 10 s to connect and 10 more to read, for each call.
    @Test fun `a server that doesn't answer can't hold the driver up`() = runTest(dispatcher) {
        SignOut().use { s ->
            s.server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))

            s.vm.onSignOut()
            dispatcher.scheduler.runCurrent()
            // Real time, without moving the test clock on: the logout is out, and nothing answers it.
            val deadline = System.nanoTime() + 5_000_000_000
            while (s.server.requestCount == 0 && System.nanoTime() < deadline) Thread.sleep(5)
            assertEquals("the logout reached the server", 1, s.server.requestCount)

            dispatcher.scheduler.advanceTimeBy(SIGN_OUT_WAIT_MS - 1)
            dispatcher.scheduler.runCurrent()
            assertFalse("still waiting, a moment short of the limit", s.vm.uiState.value.signedOut)

            dispatcher.scheduler.advanceTimeBy(1)
            awaitInRealTime("signed out at the limit") { s.vm.uiState.value.signedOut }
            assertEquals(Session(s.serverUrl, null, null), s.sessions.state.value)
        }
    }

    @Test fun `a second tap sends one logout`() = runTest(dispatcher) {
        SignOut().use { s ->
            s.server.enqueue(loggedOut())
            s.server.enqueue(loggedOut())

            s.vm.onSignOut()
            s.signOut()

            assertEquals(listOf("/api/v1/auth/logout"), s.requests().map { it.path })
        }
    }

    // They belong to the phone and its vehicles, not to the driver.
    @Test fun `favourites and recents survive`() = runTest(dispatcher) {
        val prefs = FakeVehiclePrefsStore().apply {
            favoritesState.value = setOf("4317")
            recentsState.value = listOf("4322", "4317")
        }
        val trips = FakeTripStateStore().apply { routesState.value = listOf("7", "36") }
        SignOut(trips = trips, prefs = prefs).use { s ->
            s.server.enqueue(loggedOut())

            s.signOut()

            assertEquals(setOf("4317"), prefs.favorites.first())
            assertEquals(listOf("4322", "4317"), prefs.recents.first())
            assertEquals(listOf("7", "36"), trips.recentRoutes.first())
        }
    }

    @Test fun `a store that can't be cleared still reaches Sign in`() = runTest(dispatcher) {
        val fullDisk = { store: FakeSessionStore -> store.apply { clearFailure = IOException("No space left on device") } }
        SignOut(storeFor = fullDisk).use { s ->
            s.server.enqueue(loggedOut())

            s.signOut()

            assertEquals("still stored; the server has ended it", s.signedIn, s.sessions.state.value)
        }
    }

    @Test fun `the next driver gets the server line and an empty email`() = runTest(dispatcher) {
        SignOut().use { s ->
            s.server.enqueue(loggedOut())
            s.signOut()

            val signIn = loginViewModel(sessionStore = s.store)
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(s.serverUrl, signIn.uiState.value.savedServer)
            assertNull(signIn.uiState.value.savedEmail)
            assertEquals("", signIn.email.text.toString())
            assertFalse(signIn.uiState.value.showsServerField)
        }
    }

    // Swiping the app away mid sign-out clears the view model; the driver must not be signed in at the next launch.
    @Test fun `a sign-out still forgets the driver when its screen goes first`() = runTest(dispatcher) {
        SignOut(storeFor = slowToClear).use { s ->
            s.server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
            val owner = ViewModelStore()
            val vm = ViewModelProvider.create(owner, viewModelFactory { initializer { s.vm } })[AccountViewModel::class]

            vm.onSignOut()
            dispatcher.scheduler.runCurrent()
            owner.clear()
            dispatcher.scheduler.advanceTimeBy(1)
            dispatcher.scheduler.runCurrent()

            assertEquals(Session(s.serverUrl, null, null), s.sessions.state.value)
        }
    }

    // --- Tracking screen: what the service judged, and the stored geometry's lifetime ---

    private val t1Trip = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L)

    /**
     * Builds a [TrackingViewModel] on [tripState], an active T1 unless given, with [sessions]
     * signed in, whose server answers each end with [endResponse].
     */
    private fun withTrackingViewModel(
        endResponse: MockResponse = MockResponse().setBody("""{"status":"trip ended"}"""),
        mapFiles: FakeMapFileSource = FakeMapFileSource(),
        tripState: FakeTripStateStore = FakeTripStateStore().apply { this.tripState.value = t1Trip },
        sessions: SessionStore = FakeSessionStore(),
        block: (TrackingViewModel, TrackingRepository, FakeTripGeometryStore) -> Unit,
    ) {
        val server = MockWebServer().apply { start() }
        server.enqueue(endResponse)
        try {
            val provider = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
            val tracking = TrackingRepository()
            val geometryStore = FakeTripGeometryStore().apply { stored = TripFixtures.t1 }
            val tripRepository = TripRepository(provider, tripState, FakeVehiclePrefsStore(), sessions, clock = { 0L })
            val tripEnder = TripEnder(tripRepository, tripState, geometryStore, FakeServiceController(), sessions)
            val vm = TrackingViewModel(tracking, tripState, tripEnder, mapFiles)
            awaitCondition(description = "active trip loaded") { vm.uiState.value.activeTrip != null }
            block(vm, tracking, geometryStore)
        } finally {
            server.shutdown()
        }
    }

    @Test fun `adherence the tracking service publishes reaches the screen`() = runTest(dispatcher) {
        withTrackingViewModel { vm, tracking, _ ->
            val evaluator = requireNotNull(AdherenceEvaluator.of(TripFixtures.t1))
            val adherence = evaluator.evaluate(TripFixtures.fix(47.6045, -122.3300, TripFixtures.at(8, 5)), previous = null)

            tracking.update { it.copy(geometry = TripFixtures.t1, adherence = adherence) }
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(adherence, vm.uiState.value.tracking.adherence)
            assertEquals(TripFixtures.t1, vm.uiState.value.tracking.geometry)
        }
    }

    @Test fun `the map file's state reaches the tracking screen`() = runTest(dispatcher) {
        val mapFiles = FakeMapFileSource()
        withTrackingViewModel(mapFiles = mapFiles) { vm, _, _ ->
            assertEquals(MapFileState.None, vm.uiState.value.mapFile)

            mapFiles.state.value = MapFileState.Downloading(40, 100)
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals(MapFileState.Downloading(40, 100), vm.uiState.value.mapFile)

            val file = File("/data/map/map-1.pmtiles")
            mapFiles.state.value = MapFileState.Ready(file)
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals(MapFileState.Ready(file), vm.uiState.value.mapFile)
        }
    }

    @Test fun `the tracking service is told while the screen is visible`() = runTest(dispatcher) {
        withTrackingViewModel { vm, tracking, _ ->
            assertEquals(false, tracking.liveView.value)

            vm.onScreenVisible(true)
            assertEquals(true, tracking.liveView.value)

            vm.onScreenVisible(false)
            assertEquals(false, tracking.liveView.value)
        }
    }

    @Test fun `ending a trip clears its stored geometry`() = runTest(dispatcher) {
        withTrackingViewModel { vm, _, geometryStore ->
            var ended = false
            vm.onEndTrip { ended = true }
            awaitCondition(description = "trip ended") { ended }

            assertNull(geometryStore.stored)
        }
    }

    @Test fun `a trip the server did not confirm ended keeps its geometry`() = runTest(dispatcher) {
        withTrackingViewModel(endResponse = MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START)) { vm, _, geometryStore ->
            vm.onEndTrip { fail("an unconfirmed end must not leave the screen") }
            awaitCondition(description = "end failure surfaced") { vm.uiState.value.endTripError }

            // Still running, so still judged: the driver can retry or end locally from here.
            assertEquals(TripFixtures.t1, geometryStore.stored)
        }
    }

    @Test fun `ending a trip on this device only clears its stored geometry`() = runTest(dispatcher) {
        withTrackingViewModel { vm, _, geometryStore ->
            var ended = false
            vm.onEndTripLocally { ended = true }
            awaitCondition(description = "trip ended locally") { ended }

            assertNull(geometryStore.stored)
        }
    }

    @Test fun `a trip ended on this device only after another driver signs in is kept for its own driver`() = runTest(dispatcher) {
        val tripState = FakeTripStateStore().apply { tripState.value = t1Trip.copy(driverId = "1") }
        withTrackingViewModel(tripState = tripState, sessions = signedInAs("2")) { vm, _, _ ->
            var ended = false
            vm.onEndTripLocally { ended = true }
            awaitCondition(description = "trip ended locally") { ended }

            assertEquals(listOf(UnconfirmedEnd(t1Trip.tripDbId, "1")), tripState.unconfirmedEndsState.value)
        }
    }

    // --- Resume prompt: a stored trip whose tracking service is not running ---

    private class ResumeFixture(
        val vm: ResumeShiftViewModel,
        val server: MockWebServer,
        /** Every time a repository asked for the API. Counted before any request is sent, so nothing in flight is missed. */
        val apiCalls: AtomicInteger,
        val tripState: FakeTripStateStore,
        val geometryStore: FakeTripGeometryStore,
        val serviceController: FakeServiceController,
    )

    /**
     * Builds a [ResumeShiftViewModel] on a stored T1 that started 2 h 5 min ago, whose server
     * answers each end with the next of [endResponses], and runs [block] against it.
     */
    private fun withResumeShiftViewModel(
        endResponses: List<MockResponse> = listOf(MockResponse().setBody("""{"status":"trip ended"}""")),
        geometry: TripGeometry? = TripFixtures.t1,
        block: (ResumeFixture) -> Unit,
    ) {
        val server = MockWebServer().apply { start() }
        endResponses.forEach(server::enqueue)
        try {
            val apiCalls = AtomicInteger()
            val provider = TrackerApiProvider {
                apiCalls.incrementAndGet()
                ApiFactory { "jwt" }.create(server.url("/").toString())
            }
            val tripState = FakeTripStateStore().apply { tripState.value = t1Trip }
            val geometryStore = FakeTripGeometryStore().apply { stored = geometry }
            val serviceController = FakeServiceController()
            val tripRepository = TripRepository(provider, tripState, FakeVehiclePrefsStore(), FakeSessionStore(), clock = { 0L })
            val tripEnder = TripEnder(tripRepository, tripState, geometryStore, serviceController, signedInAs("2"))
            val now = t1Trip.startedAtEpochSec + 2 * 3600 + 5 * 60
            val vm = ResumeShiftViewModel(tripState, geometryStore, serviceController, tripEnder, clock = { now }, phoneZone = ZoneOffset.UTC)
            awaitCondition(description = "stored trip loaded") { vm.uiState.value.card != null }
            block(ResumeFixture(vm, server, apiCalls, tripState, geometryStore, serviceController))
        } finally {
            server.shutdown()
        }
    }

    @Test fun showsTheTripAsTheDriverKnowsIt() = runTest(dispatcher) {
        val geometry = TripFixtures.t1.copy(routeShortName = "7", routeColor = "FDB71A", routeTextColor = "000000", headsign = "North")
        withResumeShiftViewModel(geometry = geometry) { f ->
            assertEquals(
                ResumeCard(
                    route = RouteBadgeModel.of("7", "FDB71A", "000000"),
                    headsign = "North",
                    runStartsAt = TripFixtures.t1.stops.first().departureAt,
                    vehicleId = "bus-1",
                    startedAt = Instant.ofEpochSecond(t1Trip.startedAtEpochSec),
                    zone = TripFixtures.t1.timezone,
                ),
                f.vm.uiState.value.card,
            )
            assertEquals(t1Trip.startedAtEpochSec + 2 * 3600 + 5 * 60, f.vm.nowEpochSec())
        }
    }

    @Test fun aTripStoredWithoutItsRouteNameOrHeadsignIsNamedByItsRouteId() = runTest(dispatcher) {
        // As a build before the trip bar stored it: the schedule, but "" for the route's name and the headsign.
        withResumeShiftViewModel(geometry = TripFixtures.t1) { f ->
            val card = f.vm.uiState.value.card
            assertEquals(RouteBadgeModel("R1", null, null), card?.route)
            assertNull(card?.headsign)
            assertEquals(TripFixtures.t1.stops.first().departureAt, card?.runStartsAt)
        }
    }

    @Test fun aTripWithNoScheduleOnThePhoneIsNamedByItsRouteId() = runTest(dispatcher) {
        withResumeShiftViewModel(geometry = null) { f ->
            val card = f.vm.uiState.value.card
            assertEquals(RouteBadgeModel("R1", null, null), card?.route)
            assertNull(card?.headsign)
            assertNull(card?.runStartsAt)
            assertEquals("its times are read on the phone's clock", ZoneOffset.UTC, card?.zone)
        }
    }

    @Test fun resume_startsTheServiceAndMakesNoServerCall() = runTest(dispatcher) {
        withResumeShiftViewModel { f ->
            var resumed = false
            f.vm.onResumeShift { resumed = true }
            dispatcher.scheduler.advanceUntilIdle()

            assertTrue(resumed)
            assertEquals(1, f.serviceController.startCount)
            // The trip is still open on the server, as on iOS: resuming neither starts it again
            // (that would be a 409) nor ends it, and what the phone stored stays as it was.
            assertEquals(0, f.apiCalls.get())
            assertEquals(0, f.server.requestCount)
            assertEquals(t1Trip, f.tripState.tripState.value)
            assertEquals(TripFixtures.t1, f.geometryStore.stored)
        }
    }

    @Test fun endShift_callsEndAndClearsTheTrip() = runTest(dispatcher) {
        withResumeShiftViewModel { f ->
            var ended = false
            f.vm.onEndShift { ended = true }
            awaitCondition(description = "shift ended") { ended }

            val request = f.server.takeRequest()
            assertEquals("/api/v1/trips/end", request.path)
            val body = Json.parseToJsonElement(request.body.readUtf8()).jsonObject
            assertEquals(t1Trip.tripDbId.toString(), body["trip_id"]?.jsonPrimitive?.content)
            assertNull(f.tripState.tripState.value)
            assertNull(f.geometryStore.stored)
            assertEquals(1, f.serviceController.stopCount)
            assertEquals(0, f.serviceController.startCount)
        }
    }

    @Test fun endShift_failure_surfacesTheSameErrorAsTracking() = runTest(dispatcher) {
        val refused = MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START)
        withResumeShiftViewModel(endResponses = listOf(refused)) { f ->
            f.vm.onEndShift { fail("an unconfirmed end must not leave the prompt") }
            awaitCondition(description = "end failure surfaced") { f.vm.uiState.value.endTripError }

            assertTrue(!f.vm.uiState.value.ending)
            assertEquals(t1Trip, f.tripState.tripState.value)
            assertEquals(TripFixtures.t1, f.geometryStore.stored)

            // And the same way out as the tracking screen offers: end it on this device only.
            var ended = false
            f.vm.onEndShiftLocally { ended = true }
            awaitCondition(description = "shift ended locally") { ended }
            assertTrue(!f.vm.uiState.value.endTripError)
            assertNull(f.tripState.tripState.value)
            // The server still has it open, so its id is kept for the next start to end it, with
            // the driver it belongs to.
            assertEquals(listOf(UnconfirmedEnd(t1Trip.tripDbId, "2")), f.tripState.unconfirmedEndsState.value)
            assertNull(f.geometryStore.stored)
            assertEquals(1, f.serviceController.stopCount)
        }
    }

    @Test fun `a driver who did not mean to end goes back to Resume with the trip as it was`() = runTest(dispatcher) {
        val refused = MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START)
        withResumeShiftViewModel(endResponses = listOf(refused)) { f ->
            f.vm.onEndShift { fail("an unconfirmed end must not leave the prompt") }
            awaitCondition(description = "end failure surfaced") { f.vm.uiState.value.endTripError }

            f.vm.dismissEndShiftError()
            awaitCondition(description = "back to Resume") { !f.vm.uiState.value.endTripError }

            assertEquals(t1Trip, f.tripState.tripState.value)
            assertEquals(TripFixtures.t1, f.geometryStore.stored)
            assertEquals(emptyList<UnconfirmedEnd>(), f.tripState.unconfirmedEndsState.value)
            assertEquals(0, f.serviceController.stopCount)
        }
    }

    @Test fun `a second end while one is in flight is ignored`() = runTest(dispatcher) {
        withResumeShiftViewModel { f ->
            var ends = 0
            f.vm.onEndShift { ends++ }
            // The buttons are disabled while an end is in flight, but a tap already on its way in
            // must not turn into a second POST.
            f.vm.onEndShift { ends++ }
            awaitCondition(description = "shift ended") { ends > 0 }
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(1, ends)
            assertEquals(1, f.apiCalls.get())
        }
    }

    /** A [TripEnder] whose trip store is [store], for the end on this phone, which never calls the server. */
    private fun localEnder(store: TripStateStore, serviceController: FakeServiceController = FakeServiceController()) = TripEnder(
        TripRepository(TrackerApiProvider { error("an end on this phone calls no server") }, store, FakeVehiclePrefsStore(), FakeSessionStore(), clock = { 0L }),
        store,
        FakeTripGeometryStore(),
        serviceController,
        signedInAs("2"),
    )

    @Test fun `a second end on this phone while one is in flight is ignored`() = runTest(dispatcher) {
        val written = CompletableDeferred<Unit>()
        val trips = FakeTripStateStore().apply { tripState.value = t1Trip }
        // Holds the first end at its write, as DataStore can, so the second tap lands while it is in flight.
        val slowTrips = object : TripStateStore by trips {
            override suspend fun endActiveTripLocally(signedInDriverId: String?) {
                written.await()
                trips.endActiveTripLocally(signedInDriverId)
            }
        }
        val serviceController = FakeServiceController()
        val ender = localEnder(slowTrips, serviceController)
        var ends = 0

        launch { ender.endLocally { ends++ } }
        launch { ender.endLocally { ends++ } }
        dispatcher.scheduler.advanceUntilIdle()
        assertTrue("both buttons wait while it is written", ender.state.value.ending)
        written.complete(Unit)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, ends)
        assertEquals(1, serviceController.stopCount)
        assertFalse(ender.state.value.ending)
    }

    @Test fun `an end on this phone that cannot be written says so and gives the buttons back`() = runTest(dispatcher) {
        val trips = FakeTripStateStore().apply { tripState.value = t1Trip }
        val brokenTrips = object : TripStateStore by trips {
            override suspend fun endActiveTripLocally(signedInDriverId: String?) = throw IOException("disk full")
        }
        val ender = localEnder(brokenTrips)

        // Returning at all is the point: both callers launch this with no handler, so a throw would crash the app.
        ender.endLocally { fail("an end that was not written must not leave the screen") }

        assertTrue(ender.state.value.failed)
        assertFalse(ender.state.value.ending)
    }

    @Test fun `a cancelled end on this phone is passed on, not shown as failed`() = runTest(dispatcher) {
        val trips = FakeTripStateStore().apply { tripState.value = t1Trip }
        val cancelledTrips = object : TripStateStore by trips {
            override suspend fun endActiveTripLocally(signedInDriverId: String?) = throw CancellationException("screen left")
        }
        val ender = localEnder(cancelledTrips)

        val failure = runCatching { ender.endLocally { fail("a cancelled end must not leave the screen") } }.exceptionOrNull()

        assertTrue("expected the cancellation, got $failure", failure is CancellationException)
        assertFalse(ender.state.value.failed)
    }
}
