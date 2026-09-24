package org.onebusaway.vehicletracker.ui

import app.cash.turbine.test
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.*
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.ui.login.LoginError
import org.onebusaway.vehicletracker.ui.login.LoginViewModel
import org.onebusaway.vehicletracker.ui.routes.RoutesUiState
import org.onebusaway.vehicletracker.ui.routes.RoutesViewModel
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.RunsViewModel
import org.onebusaway.vehicletracker.ui.runs.TripError
import org.onebusaway.vehicletracker.ui.vehicles.VehicleViewModel
import org.onebusaway.vehicletracker.ui.vehicles.VehiclesUiState
import java.time.OffsetDateTime
import java.time.ZoneOffset

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

    @Test fun `login rejects blank fields without calling network`() = runTest(dispatcher) {
        val sessionStore = FakeSessionStore()
        val vm = LoginViewModel(AuthRepository(sessionStore, ApiFactory { null }, clock = { 0L }), sessionStore)
        vm.onServerUrlChange(""); vm.onEmailChange(""); vm.onPasswordChange("")
        var succeeded = false
        vm.onLogin { succeeded = true }
        dispatcher.scheduler.advanceUntilIdle()
        assertTrue(!succeeded)
        assertEquals(LoginError.OTHER, vm.uiState.value.error)
    }

    @Test fun `login success invokes callback`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody("""{"token":"jwt"}"""))
        val sessionStore = FakeSessionStore()
        val vm = LoginViewModel(AuthRepository(sessionStore, ApiFactory { null }, clock = { 0L }), sessionStore)
        vm.onServerUrlChange(server.url("/").toString())
        vm.onEmailChange("d@example.com"); vm.onPasswordChange("pw")
        var succeeded = false
        vm.onLogin { succeeded = true }
        awaitCondition(description = "login onSuccess callback invoked") { succeeded }
        assertTrue(succeeded)
        server.shutdown()
    }

    @Test fun `login prefills server url from session store`() = runTest(dispatcher) {
        val sessionStore = FakeSessionStore().apply {
            state.value = Session("https://saved.example.com", null, null)
        }
        val vm = LoginViewModel(AuthRepository(sessionStore, ApiFactory { null }, clock = { 0L }), sessionStore)
        vm.uiState.test {
            var state = awaitItem()
            while (state.serverUrl.isEmpty()) state = awaitItem()
            assertEquals("https://saved.example.com", state.serverUrl)
            cancelAndIgnoreRemainingEvents()
        }
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

    /** Applies pending ViewModel work, then reads the ids currently on screen, in order. */
    private fun visibleIds(vm: VehicleViewModel): List<String> {
        dispatcher.scheduler.advanceUntilIdle()
        return loadedState(vm).visible.map { it.id }
    }

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
            assertEquals(setOf("bus-3"), loadedState(vm).favorites)
            assertEquals("bus-3", visibleIds(vm).first())

            vm.onToggleFavorite("bus-3")
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals(emptySet<String>(), loadedState(vm).favorites)
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

    /**
     * Builds a [RunsViewModel] over a `MockWebServer` answering the trips call with [tripsBody]
     * and then each of [startResponses] in turn, loads route R1 at wall-clock time [now], and
     * runs [block] against it.
     */
    private fun withRunsViewModel(
        tripsBody: String = twoRunsJson,
        now: String = "2026-09-22T08:05:00-07:00",
        startResponses: List<MockResponse> = emptyList(),
        block: (RunsViewModel, MockWebServer, FakeServiceController) -> Unit,
    ) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setBody(tripsBody))
        startResponses.forEach { server.enqueue(it) }
        try {
            val provider = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
            val nowEpochSec = OffsetDateTime.parse(now).toEpochSecond()
            val clock = { nowEpochSec }
            val tripRepository = TripRepository(
                provider,
                FakeTripStateStore(),
                FakeVehiclePrefsStore(),
                clock = clock,
                zone = ZoneOffset.UTC,
            )
            val serviceController = FakeServiceController()
            val vm = RunsViewModel(CatalogRepository(provider), tripRepository, serviceController, clock)
            vm.load("R1")
            awaitCondition(description = "runs settled") { vm.uiState.value !is RunsUiState.Loading }
            block(vm, server, serviceController)
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
        withRunsViewModel(now = "2026-09-22T08:05:00-07:00") { vm, _, _ ->
            val state = loadedRuns(vm)
            assertEquals("T1", state.highlightedId)
            assertEquals(RunHighlight.NOW, state.highlight)
            // The zone is the agency's, read off the response rather than off the device.
            assertEquals("America/Los_Angeles", state.page.timezone.id)
        }
    }

    @Test fun `the next run to start is badged Next once the earlier one is over`() = runTest(dispatcher) {
        withRunsViewModel(now = "2026-09-22T09:00:00-07:00") { vm, _, _ ->
            val state = loadedRuns(vm)
            assertEquals("T4", state.highlightedId)
            assertEquals(RunHighlight.NEXT, state.highlight)
        }
    }

    @Test fun `a route with no runs today loads empty rather than failing`() = runTest(dispatcher) {
        val body = """{"route_id":"R1","service_date":"20260922","timezone":"America/Los_Angeles","trips":[]}"""
        withRunsViewModel(tripsBody = body) { vm, _, _ ->
            // A schedule exists, this route just is not running today — not a reason to doubt
            // the catalog.
            val state = loadedRuns(vm)
            assertEquals(emptyList<String>(), state.page.runs.map { it.id })
            assertNull(state.highlightedId)
        }
    }

    @Test fun `starting a run sends the catalog's route id and trip id, then starts tracking`() = runTest(dispatcher) {
        val started = MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}""",
        )
        withRunsViewModel(startResponses = listOf(started)) { vm, server, serviceController ->
            var navigated = false
            vm.onStartRun("bus-1", "T1") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            assertEquals(1, serviceController.startCount)
            server.takeRequest() // the trips call
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
        withRunsViewModel(startResponses = refusals) { vm, _, serviceController ->
            vm.onStartRun("bus-1", "T1") { }
            awaitCondition(description = "403 surfaced") { loadedRuns(vm).error != null }
            assertEquals(TripError.NOT_ASSIGNED, loadedRuns(vm).error)

            vm.onStartRun("bus-1", "T1") { }
            awaitCondition(description = "409 surfaced") { loadedRuns(vm).error == TripError.TRIP_ACTIVE }
            assertEquals(0, serviceController.startCount)
        }
    }

    @Test fun `a second tap while a start is in flight is ignored`() = runTest(dispatcher) {
        val started = MockResponse().setResponseCode(201).setBody(
            """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}""",
        )
        withRunsViewModel(startResponses = listOf(started)) { vm, server, _ ->
            var navigated = false
            vm.onStartRun("bus-1", "T1") { navigated = true }
            // The rows are disabled while a start is in flight, but a tap already on its way in
            // must not turn into a second POST — the server would answer that one 409.
            vm.onStartRun("bus-1", "T4") { navigated = true }
            awaitCondition(description = "trip started") { navigated }

            assertEquals(2, server.requestCount)
        }
    }
}
