package org.onebusaway.vehicletracker.ui

import androidx.lifecycle.SavedStateHandle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.CatalogRepository
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeServiceController
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripGeometryStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FakeVehiclePrefsStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.service.FakeBacklogUploadScheduler
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.RunsViewModel
import java.time.OffsetDateTime

/**
 * The server can confirm a start after the driver has locked the phone. From Android 12 Android then refuses the
 * tracking service, which runs only with the app in front.
 */
class RunStartRefusedTest {
    private val dispatcher = StandardTestDispatcher()
    private val server = MockWebServer()
    private val runsJson =
        """{"route_id":"R1","service_date":"20260922","timezone":"America/Los_Angeles","trips":[""" +
            """{"id":"T1","headsign":"North","direction_id":0,"starts_at":"2026-09-22T08:00:00-07:00",""" +
            """"ends_at":"2026-09-22T08:10:00-07:00","first_stop":"Stop ST1","last_stop":"Stop ST3"}]}"""
    private val started =
        """{"id":7,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-22T15:00:00Z","status":"active"}"""

    @Before fun setUp() {
        Dispatchers.setMain(dispatcher)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when {
                request.path == "/api/v1/trips/start" -> MockResponse().setResponseCode(201).setBody(started)
                request.path.orEmpty().startsWith("/api/v1/gtfs/trips/") ->
                    MockResponse().setBody(TripFixtures.tripJson(serviceDate = "20260922"))
                else -> MockResponse().setBody(runsJson)
            }
        }
        server.start()
    }

    @After fun tearDown() {
        server.shutdown()
        Dispatchers.resetMain()
    }

    /** Polls [condition] while running [dispatcher]: the replies come back on OkHttp's own threads. */
    private fun awaitCondition(description: String, condition: () -> Boolean) {
        val deadline = System.nanoTime() + 5_000_000_000
        while (System.nanoTime() < deadline) {
            dispatcher.scheduler.advanceUntilIdle()
            if (condition()) return
            Thread.sleep(5)
        }
        throw AssertionError("timed out waiting for $description")
    }

    @Test fun `a start Android refuses waits on Starting and reports once the step is back on screen`() = runTest(dispatcher) {
        val provider = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
        val clock = { OffsetDateTime.parse("2026-09-22T07:58:00-07:00").toEpochSecond() }
        val sessionStore = FakeSessionStore()
        val trips = FakeTripStateStore()
        val services = FakeServiceController().apply { startFailure = IllegalStateException("startForegroundService() not allowed") }
        val vm = RunsViewModel(
            SavedStateHandle(mapOf("vehicleId" to "bus-1", "routeId" to "R1")),
            CatalogRepository(provider),
            TripRepository(provider, trips, FakeVehiclePrefsStore(), sessionStore, clock = clock),
            FakeTripGeometryStore(),
            services,
            AuthRepository(sessionStore, trips, ApiFactory { null }, provider, FakeOfflineFixQueue(), FakeBacklogUploadScheduler(), clock = clock),
            clock,
        )
        awaitCondition("runs loaded") { vm.uiState.value is RunsUiState.Loaded }
        var left = 0

        vm.onStartRun("T1") { left++ }
        awaitCondition("start refused") { services.startCount == 1 }

        assertNotNull("the trip is open on the server", trips.tripState.value)
        assertEquals(0, left)
        assertEquals("T1", (vm.uiState.value as RunsUiState.Loaded).startingRunId)
        assertEquals("Reports paused", 1, services.askCount)

        services.startFailure = null
        vm.onScreenVisible(true)
        try {
            dispatcher.scheduler.runCurrent()
            assertEquals(2, services.startCount)
            assertEquals(1, left)
            assertEquals(null, (vm.uiState.value as RunsUiState.Loaded).startingRunId)
            assertEquals(1, services.askCount)
        } finally {
            vm.onScreenVisible(false)
        }
    }
}
