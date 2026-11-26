package org.onebusaway.vehicletracker.ui.settings

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.map.MapDetails
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.map.OfflineMapSource
import java.io.File

/** The map's row and page as the map's state changes, and what the page's buttons ask of it. */
class OfflineMapViewModelTest {
    private val dispatcher = StandardTestDispatcher()
    private val details = MapDetails(48_000_000, 1_789_000_000_000L, 1_790_000_000L)

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private class FakeSource(var details: MapDetails?) : OfflineMapSource {
        override val state = MutableStateFlow<MapFileState>(MapFileState.None)
        val asked = mutableListOf<String>()
        var detailsRead = 0

        override suspend fun details(): MapDetails? = details.also { detailsRead++ }
        override fun refresh() { asked += "refresh" }
        override fun delete() { asked += "delete" }
        override fun getAgain() { asked += "getAgain" }
    }

    @Test fun `read from the phone before it is shown`() = runTest(dispatcher) {
        val vm = OfflineMapViewModel(FakeSource(details))
        assertEquals(OfflineMapView.Reading, vm.map.value)

        advanceUntilIdle()
        assertEquals(OfflineMapView.Read(null), vm.map.value)
    }

    @Test fun `a map here shows its details, read again as it changes`() = runTest(dispatcher) {
        val source = FakeSource(details)
        val vm = OfflineMapViewModel(source)
        source.state.value = MapFileState.Ready(File("map-a.pmtiles"))
        advanceUntilIdle()
        assertEquals(OfflineMapView.Read(OfflineMap.Ready(details)), vm.map.value)

        val newer = details.copy(bytes = 50_000_000)
        source.details = newer
        source.state.value = MapFileState.Ready(File("map-b.pmtiles"))
        advanceUntilIdle()
        assertEquals(OfflineMapView.Read(OfflineMap.Ready(newer)), vm.map.value)
    }

    @Test fun `a download's progress reads nothing from the disk`() = runTest(dispatcher) {
        val source = FakeSource(details)
        val vm = OfflineMapViewModel(source)
        advanceUntilIdle()
        val read = source.detailsRead

        source.state.value = MapFileState.Downloading(20, 48)
        advanceUntilIdle()
        source.state.value = MapFileState.Ready(File("map-a.pmtiles"), update = MapFileState.Downloading(30, 48))
        advanceUntilIdle()

        assertEquals(OfflineMapView.Read(OfflineMap.Downloading(30, 48)), vm.map.value)
        assertEquals(read, source.detailsRead)
    }

    @Test fun `each button asks the map for its own`() = runTest(dispatcher) {
        val source = FakeSource(details)
        val vm = OfflineMapViewModel(source)

        vm.onGetAgain()
        vm.onDelete()
        vm.onTryAgain()

        assertEquals(listOf("getAgain", "delete", "refresh"), source.asked)
    }
}
