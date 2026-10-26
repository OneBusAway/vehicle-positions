package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
import androidx.compose.ui.graphics.Color
import androidx.lifecycle.SavedStateHandle
import app.cash.turbine.test
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
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
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.login.LoginError
import org.onebusaway.vehicletracker.ui.login.LoginField
import org.onebusaway.vehicletracker.ui.login.LoginUiState
import org.onebusaway.vehicletracker.ui.login.LoginViewModel
import org.onebusaway.vehicletracker.ui.login.bannerOf
import org.onebusaway.vehicletracker.ui.login.fieldErrorRes
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
import org.onebusaway.vehicletracker.ui.resume.ResumeCard
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftViewModel
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import org.onebusaway.vehicletracker.ui.vehicles.BusSection
import org.onebusaway.vehicletracker.ui.vehicles.VehicleViewModel
import org.onebusaway.vehicletracker.ui.vehicles.VehiclesUiState
import java.io.File
import java.io.IOException
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.util.concurrent.ConcurrentLinkedQueue
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

    // --- Sign in ---

    private fun loginViewModel(
        sessionStore: SessionStore = FakeSessionStore(),
        tripState: FakeTripStateStore = FakeTripStateStore(),
        geometryStore: FakeTripGeometryStore = FakeTripGeometryStore(),
        again: Boolean = false,
    ) = LoginViewModel(
        AuthRepository(sessionStore, ApiFactory { null }, clock = { 0L }),
        sessionStore,
        tripState,
        geometryStore,
        // As the nav graph writes it: only the route Tracking opens sets it.
        SavedStateHandle(if (again) mapOf(ARG_SIGN_IN_AGAIN to true) else emptyMap()),
    )

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

    private fun failedSignIn(response: MockResponse): LoginUiState {
        val server = MockWebServer().apply { start() }
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

    @Test fun `an address that is not http is unreachable without asking anyone`() = runTest(dispatcher) {
        val vm = loginViewModel()
        dispatcher.scheduler.advanceUntilIdle()
        vm.fill("tracker.example.org", "d@example.com", "pw")
        vm.onSignIn { fail("there is no server to sign in to") }
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(LoginError.Unreachable("tracker.example.org"), vm.uiState.value.error)
        assertFalse(vm.uiState.value.loading)
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
        }
        assertEquals(R.string.login_error_missing_server, fieldErrorRes(LoginError.MissingField(LoginField.SERVER), LoginField.SERVER))
        assertEquals(R.string.login_error_missing_email, fieldErrorRes(LoginError.MissingField(LoginField.EMAIL), LoginField.EMAIL))
        assertEquals(R.string.login_error_missing_password, fieldErrorRes(LoginError.MissingField(LoginField.PASSWORD), LoginField.PASSWORD))
        assertNull(fieldErrorRes(LoginError.MissingField(LoginField.EMAIL), LoginField.PASSWORD))
        assertEquals(R.string.login_error_invalid_credentials, fieldErrorRes(LoginError.InvalidCredentials, LoginField.PASSWORD))
        assertNull(fieldErrorRes(LoginError.InvalidCredentials, LoginField.EMAIL))
        assertEquals(R.string.login_error_check_address, fieldErrorRes(LoginError.Unreachable("x"), LoginField.SERVER))
        assertNull(fieldErrorRes(LoginError.Unreachable("x"), LoginField.PASSWORD))

        assertEquals(R.string.login_error_unreachable, bannerOf(LoginError.Unreachable("x"))?.text)
        assertEquals(R.string.login_error_server, bannerOf(LoginError.ServerError)?.text)
        assertNull(bannerOf(LoginError.InvalidCredentials))
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
    ) = AppNavViewModel(
        FakeSessionStore().apply { state.value = session },
        FakeTripStateStore().apply { tripState.value = trip },
        tracking,
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
            val vm = VehicleViewModel(repo, prefs)
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
            val vm = RoutesViewModel(catalogFor(server), store)
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

    @Test fun `a failed route load offers a retry, and a 401 does not`() = runTest(dispatcher) {
        withRoutesViewModel(response = MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START)) { vm ->
            assertEquals(RoutesUiState.Error(retry = true), vm.uiState.value)
        }
        withRoutesViewModel(response = MockResponse().setResponseCode(401).setBody("""{"error":"bad token"}""")) { vm ->
            // Retrying with the same dead token only fails again; the driver has to sign in.
            assertEquals(RoutesUiState.Error(retry = false), vm.uiState.value)
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
            val tripRepository = TripRepository(provider, tripState, FakeVehiclePrefsStore(), FakeSessionStore(), clock = clock)
            val serviceController = FakeServiceController()
            // The nav graph's arguments, which is where the ViewModel reads them from — and it
            // loads off them in init, with no load() call from the screen.
            val args = SavedStateHandle(mapOf("vehicleId" to "bus-1", "routeId" to "R1") + extraArgs)
            val vm = RunsViewModel(args, CatalogRepository(provider), tripRepository, geometryStore, serviceController, clock)
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
        withRunsViewModel(startResponses = refusals) { vm, _, serviceController, _ ->
            vm.onStartRun("T1") { }
            awaitCondition(description = "403 surfaced") { loadedRuns(vm).error != null }
            assertEquals(TripError.NOT_ASSIGNED, loadedRuns(vm).error)

            vm.onStartRun("T1") { }
            awaitCondition(description = "409 surfaced") { loadedRuns(vm).error == TripError.TRIP_ACTIVE }
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

    @Test fun `an end on this phone that cannot be written gives the buttons back`() = runTest(dispatcher) {
        val trips = FakeTripStateStore().apply { tripState.value = t1Trip }
        val brokenTrips = object : TripStateStore by trips {
            override suspend fun endActiveTripLocally(signedInDriverId: String?) = throw IOException("disk full")
        }
        val ender = localEnder(brokenTrips)

        val failure = runCatching { ender.endLocally { fail("an end that was not written must not leave the screen") } }.exceptionOrNull()

        assertTrue("expected the write's IOException, got $failure", failure is IOException)
        assertFalse(ender.state.value.ending)
    }
}
