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
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.*
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.ui.login.LoginError
import org.onebusaway.vehicletracker.ui.login.LoginViewModel
import org.onebusaway.vehicletracker.ui.trip.TripSetupViewModel

class ViewModelsTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)
    @After fun tearDown() = Dispatchers.resetMain()

    @Test fun `login rejects blank fields without calling network`() = runTest(dispatcher) {
        val vm = LoginViewModel(AuthRepository(FakeSessionStore(), ApiFactory { null }, clock = { 0L }))
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
        val vm = LoginViewModel(AuthRepository(FakeSessionStore(), ApiFactory { null }, clock = { 0L }))
        vm.onServerUrlChange(server.url("/").toString())
        vm.onEmailChange("d@example.com"); vm.onPasswordChange("pw")
        var succeeded = false
        vm.onLogin { succeeded = true }
        dispatcher.scheduler.advanceUntilIdle()
        assertTrue(succeeded)
        server.shutdown()
    }

    @Test fun `trip setup maps 403 to NOT_ASSIGNED error`() = runTest(dispatcher) {
        val server = MockWebServer().apply { start() }
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"not assigned"}"""))
        val store = FakeTripStateStore()
        val repo = TripRepository(ApiFactory { "jwt" }.create(server.url("/").toString()), store, clock = { 0L })
        val vm = TripSetupViewModel(repo, store)
        vm.onRouteIdChange("5")
        vm.onStartTrip("bus-1") { }
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(org.onebusaway.vehicletracker.ui.trip.TripError.NOT_ASSIGNED, vm.uiState.value.error)
        server.shutdown()
    }

    @Test fun `trip setup exposes recent routes`() = runTest(dispatcher) {
        val store = FakeTripStateStore()
        store.addRecentRoute("12"); store.addRecentRoute("5")
        val server = MockWebServer().apply { start() }
        val repo = TripRepository(ApiFactory { "jwt" }.create(server.url("/").toString()), store, clock = { 0L })
        val vm = TripSetupViewModel(repo, store)
        vm.uiState.test {
            var state = awaitItem()
            while (state.recentRoutes.isEmpty()) state = awaitItem()
            assertEquals(listOf("5", "12"), state.recentRoutes)
            cancelAndIgnoreRemainingEvents()
        }
        server.shutdown()
    }
}
