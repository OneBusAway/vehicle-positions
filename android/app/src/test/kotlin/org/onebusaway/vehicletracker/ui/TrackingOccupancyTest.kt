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
import org.onebusaway.vehicletracker.data.FakeServiceController
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripGeometryStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FakeVehiclePrefsStore
import org.onebusaway.vehicletracker.data.OccupancyStatus
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import java.io.IOException

/** The occupancy row's taps on the Tracking screen, which set how full the vehicle is on the trip. */
class TrackingOccupancyTest {
    private val dispatcher = StandardTestDispatcher()
    private val trips = FakeTripStateStore().apply { tripState.value = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L) }

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private fun TestScope.viewModelOn(store: TripStateStore): TrackingViewModel {
        val sessions = FakeSessionStore()
        val api = TrackerApiProvider { error("ending the trip is not part of these tests") }
        val ender = TripEnder(TripRepository(api, store, FakeVehiclePrefsStore(), sessions, clock = { 0L }), store, FakeTripGeometryStore(), FakeServiceController(), sessions)
        return TrackingViewModel(TrackingRepository(), store, ender, FakeMapFileSource()).also { advanceUntilIdle() }
    }

    private fun TestScope.tap(vm: TrackingViewModel, occupancy: OccupancyStatus): OccupancyStatus? {
        vm.onOccupancyClick(occupancy)
        advanceUntilIdle()
        assertEquals("the screen shows what is stored", trips.tripState.value?.occupancy, vm.uiState.value.activeTrip?.occupancy)
        return trips.tripState.value?.occupancy
    }

    @Test fun `a tap sets the trip's occupancy, another changes it, and tapping it again clears it`() = runTest(dispatcher) {
        val vm = viewModelOn(trips)

        assertEquals(OccupancyStatus.FULL, tap(vm, OccupancyStatus.FULL))
        assertEquals(OccupancyStatus.STANDING_ROOM_ONLY, tap(vm, OccupancyStatus.STANDING_ROOM_ONLY))
        assertNull(tap(vm, OccupancyStatus.STANDING_ROOM_ONLY))
        assertEquals(OccupancyStatus.MANY_SEATS_AVAILABLE, tap(vm, OccupancyStatus.MANY_SEATS_AVAILABLE))
    }

    @Test fun `a tap the phone can't store is dropped, not thrown`() = runTest(dispatcher) {
        val full = object : TripStateStore by trips {
            override suspend fun setOccupancy(tripDbId: Long, occupancy: OccupancyStatus?) = throw IOException("No space left on device")
        }
        val vm = viewModelOn(full)

        vm.onOccupancyClick(OccupancyStatus.FULL)
        advanceUntilIdle()

        assertNull(vm.uiState.value.activeTrip?.occupancy)
    }
}
