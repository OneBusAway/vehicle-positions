package org.onebusaway.vehicletracker.ui.settings

import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.DataStoreTripSettingsStore
import org.onebusaway.vehicletracker.data.FailingDataStore
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.ScreenOn
import org.onebusaway.vehicletracker.data.TripSettings
import org.onebusaway.vehicletracker.data.TripSettingsStore
import java.io.IOException

/** The choices for Tracking as they are read and saved, and as the phone can do neither. */
class TripSettingsViewModelTest {
    private val dispatcher = StandardTestDispatcher()
    private val saved = TripSettings(ScreenOn.ALWAYS, MapUp.NORTH, vibrates = false)

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private open class FakeStore(initial: TripSettings) : TripSettingsStore {
        val state = MutableStateFlow(initial)
        override val settings: Flow<TripSettings> = state
        override suspend fun setScreenOn(screenOn: ScreenOn) { state.value = state.value.copy(screenOn = screenOn) }
        override suspend fun setMapUp(mapUp: MapUp) { state.value = state.value.copy(mapUp = mapUp) }
        override suspend fun setVibrates(vibrates: Boolean) { state.value = state.value.copy(vibrates = vibrates) }
    }

    @Test fun `there is no choice until the saved one is read`() = runTest(dispatcher) {
        val vm = TripSettingsViewModel(FakeStore(saved))
        assertNull(vm.settings.value)

        advanceUntilIdle()
        assertEquals(saved, vm.settings.value)
    }

    @Test fun `each choice is saved`() = runTest(dispatcher) {
        val vm = TripSettingsViewModel(FakeStore(TripSettings()))

        vm.onScreenOn(ScreenOn.ALWAYS)
        vm.onMapUp(MapUp.NORTH)
        vm.onVibrates(false)
        advanceUntilIdle()

        assertEquals(saved, vm.settings.value)
    }

    @Test fun `a choice the phone can't save is dropped, not thrown, and the saved one stays`() = runTest(dispatcher) {
        val vm = TripSettingsViewModel(object : FakeStore(saved) {
            override suspend fun setScreenOn(screenOn: ScreenOn) = throw IOException("No space left on device")
            override suspend fun setMapUp(mapUp: MapUp) = throw IOException("No space left on device")
            override suspend fun setVibrates(vibrates: Boolean) = throw IOException("No space left on device")
        })
        advanceUntilIdle()

        vm.onScreenOn(ScreenOn.CHARGING)
        advanceUntilIdle()
        assertEquals(saved, vm.settings.value)
        vm.onMapUp(MapUp.HEADING)
        advanceUntilIdle()
        assertEquals(saved, vm.settings.value)
        vm.onVibrates(true)
        advanceUntilIdle()
        assertEquals(saved, vm.settings.value)
    }

    // The store reads again every few seconds for as long as it is collected, so virtual time never runs out of work:
    // runCurrent, not advanceUntilIdle, and the view model's scope ends with the test.
    @Test fun `choices the phone can't read are the defaults, and a tap is dropped`() = runTest(dispatcher) {
        val vm = TripSettingsViewModel(DataStoreTripSettingsStore(FailingDataStore(IOException("the file is gone"))))
        runCurrent()
        assertEquals(TripSettings(), vm.settings.value)

        vm.onVibrates(false)
        runCurrent()
        assertEquals(TripSettings(), vm.settings.value)
        vm.viewModelScope.cancel()
    }
}
