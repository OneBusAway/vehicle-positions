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

    @Test fun `trip setup maps 403 to NOT_ASSIGNED error`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"not assigned"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }, store, clock = { 0L }, zone = ZoneOffset.UTC)
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
        val repo = TripRepository(TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }, store, clock = { 0L }, zone = ZoneOffset.UTC)
        val vm = TripSetupViewModel(repo, store, FakeServiceController())
        vm.uiState.test {
            var state = awaitItem()
            while (state.recentRoutes.isEmpty()) state = awaitItem()
            assertEquals(listOf("5", "12"), state.recentRoutes)
            cancelAndIgnoreRemainingEvents()
        }
        server.shutdown()
    }
}
