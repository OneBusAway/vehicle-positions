package org.onebusaway.vehicletracker.ui

import app.cash.turbine.test
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
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
import org.onebusaway.vehicletracker.ui.trip.TripSetupViewModel
import org.onebusaway.vehicletracker.ui.vehicles.VehicleViewModel
import org.onebusaway.vehicletracker.ui.vehicles.VehiclesUiState

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

    @Test fun `trip setup maps 403 to NOT_ASSIGNED error`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"not assigned"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }, store, FakeVehiclePrefsStore(), clock = { 0L })
        val vm = TripSetupViewModel(repo, store, FakeServiceController())
        vm.onRouteIdChange("5")
        vm.onStartTrip("bus-1") { }
        awaitCondition(description = "trip setup error set") { vm.uiState.value.error != null }
        assertEquals(org.onebusaway.vehicletracker.ui.trip.TripError.NOT_ASSIGNED, vm.uiState.value.error)
        server.shutdown()
    }

    @Test fun `trip setup exposes recent routes`() = runTest(dispatcher) {
        val store = FakeTripStateStore()
        store.addRecentRoute("12"); store.addRecentRoute("5")
        val server = MockWebServer().apply { start() }
        val repo = TripRepository(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }, store, FakeVehiclePrefsStore(), clock = { 0L })
        val vm = TripSetupViewModel(repo, store, FakeServiceController())
        vm.uiState.test {
            var state = awaitItem()
            while (state.recentRoutes.isEmpty()) state = awaitItem()
            assertEquals(listOf("5", "12"), state.recentRoutes)
            cancelAndIgnoreRemainingEvents()
        }
        server.shutdown()
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
}
