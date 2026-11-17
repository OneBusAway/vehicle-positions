package org.onebusaway.vehicletracker.ui

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.FakeMapFileSource
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeServiceController
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripGeometryStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FakeVehiclePrefsStore
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.data.WaitingReports
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.offlineQueueAccount
import org.onebusaway.vehicletracker.service.queuedFix
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder

/** The reports waiting in the queue reach the Tracking screen's state, as they come and as they upload. */
class TrackingWaitingTest {
    private val dispatcher = StandardTestDispatcher()
    private val trips = FakeTripStateStore().apply { tripState.value = ActiveTrip(7L, "T1", "van-1", "R1", "20260902", 100L) }
    private val sessions = FakeSessionStore().apply {
        state.value = Session(serverUrl = "https://tracker.example.org", token = "jwt", issuedAtEpochSec = 0L, email = "d@example.com")
    }
    private val queue = FakeOfflineFixQueue()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private fun TestScope.viewModel(): TrackingViewModel {
        val api = TrackerApiProvider { error("ending the trip is not part of these tests") }
        val tripRepository = TripRepository(api, trips, FakeVehiclePrefsStore(), sessions, clock = { 0L })
        val ender = TripEnder(tripRepository, trips, FakeTripGeometryStore(), FakeServiceController(), sessions)
        return TrackingViewModel(TrackingRepository(), trips, ender, FakeMapFileSource(), WaitingReports(queue, sessions))
            .also { advanceUntilIdle() }
    }

    @Test fun `the count goes from the queue to the screen, from 0 to 1 to many and back to 0`() = runTest(dispatcher) {
        queue.claimFor(offlineQueueAccount("https://tracker.example.org", "d@example.com"))
        val vm = viewModel()
        assertNull("nothing queued", vm.uiState.value.waiting)

        queue.add(queuedFix("van-1", timestamp = 1L))
        advanceUntilIdle()
        assertEquals(ReportsWaiting(1, WaitingOutlook.WILL_SEND), vm.uiState.value.waiting)

        (2L..37L).forEach { queue.add(queuedFix("van-1", timestamp = it)) }
        advanceUntilIdle()
        assertEquals(ReportsWaiting(37, WaitingOutlook.WILL_SEND), vm.uiState.value.waiting)

        queue.remove(queue.oldest(37))
        advanceUntilIdle()
        assertNull("all uploaded", vm.uiState.value.waiting)
    }

    @Test fun `reports saved under another sign-in reach the screen as never sending`() = runTest(dispatcher) {
        queue.claimFor(offlineQueueAccount("https://tracker.example.org", "e@example.com"))
        queue.add(queuedFix("van-1"))

        val vm = viewModel()

        assertEquals(ReportsWaiting(1, WaitingOutlook.NEVER), vm.uiState.value.waiting)
    }
}
