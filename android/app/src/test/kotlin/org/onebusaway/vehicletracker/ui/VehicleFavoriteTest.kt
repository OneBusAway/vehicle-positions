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
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FakeVehiclePrefsStore
import org.onebusaway.vehicletracker.data.VehiclePrefsStore
import org.onebusaway.vehicletracker.data.VehicleRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.service.FakeBacklogUploadScheduler
import org.onebusaway.vehicletracker.ui.vehicles.VehicleViewModel
import java.io.IOException

/** The star on a vehicle's tile, which keeps the vehicle at the top of the vehicle step. */
class VehicleFavoriteTest {
    private val dispatcher = StandardTestDispatcher()
    private val prefs = FakeVehiclePrefsStore()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private fun TestScope.viewModelOn(store: VehiclePrefsStore): VehicleViewModel {
        val sessions = FakeSessionStore()
        val api = TrackerApiProvider { error("loading the vehicles is not part of these tests") }
        val auth = AuthRepository(sessions, FakeTripStateStore(), ApiFactory { null }, api, FakeOfflineFixQueue(), FakeBacklogUploadScheduler(), clock = { 0L })
        return VehicleViewModel(VehicleRepository(api, sessions), store, auth).also { advanceUntilIdle() }
    }

    @Test fun `a star the phone can't store is dropped, not thrown`() = runTest(dispatcher) {
        var diskFull = true
        val store = object : VehiclePrefsStore by prefs {
            override suspend fun toggleFavorite(vehicleId: String) =
                if (diskFull) throw IOException("No space left on device") else prefs.toggleFavorite(vehicleId)
        }
        val vm = viewModelOn(store)

        vm.onToggleFavorite("vehicle-1")
        advanceUntilIdle()
        assertEquals("the tap that couldn't be stored changed nothing", emptySet<String>(), prefs.favoritesState.value)

        diskFull = false
        vm.onToggleFavorite("vehicle-1")
        advanceUntilIdle()
        assertEquals("the next one, once there is room, is stored", setOf("vehicle-1"), prefs.favoritesState.value)
    }
}
