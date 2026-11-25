package org.onebusaway.vehicletracker.ui

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
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
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.WaitingReports
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import org.onebusaway.vehicletracker.ui.tracking.vibratesFor

/**
 * The Tracking screen's state from its first one, with the trip under way before it opened and the stored trip still to
 * be read: a problem there already is not one that comes up.
 */
class TrackingOpensTest {
    private val dispatcher = StandardTestDispatcher()
    private val tracking = TrackingRepository()
    private val mapFiles = FakeMapFileSource()
    private val stored = FakeTripStateStore().apply { tripState.value = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L) }

    /** Read from the disk, so after the screen has drawn its first state. */
    private val trips = object : TripStateStore by stored {
        override val activeTrip: Flow<ActiveTrip?> = flow {
            delay(READ_MS)
            emitAll(stored.activeTrip)
        }
    }

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private fun viewModel(): TrackingViewModel {
        val sessions = FakeSessionStore()
        val api = TrackerApiProvider { error("ending the trip is not part of these tests") }
        val ender = TripEnder(TripRepository(api, trips, FakeVehiclePrefsStore(), sessions, clock = { 0L }), trips, FakeTripGeometryStore(), FakeServiceController(), sessions)
        return TrackingViewModel(tracking, trips, ender, mapFiles, WaitingReports(FakeOfflineFixQueue(), sessions, clock = { 0L }))
    }

    /** The problems the screen shows one after another, its first state's first, as the screen would draw them. */
    private fun TestScope.problemsOn(vm: TrackingViewModel): List<TrackingProblem> {
        val shown = mutableListOf(vm.uiState.value.tracking.problem)
        backgroundScope.launch { vm.uiState.collect { shown += it.tracking.problem } }
        runCurrent()
        return shown
    }

    private fun vibrations(shown: List<TrackingProblem>): Int = shown.zipWithNext().count { (before, problem) -> vibratesFor(before, problem) }

    private fun problem(problem: TrackingProblem) = tracking.update { it.copy(active = true, problem = problem) }

    @Test fun `a problem there as the screen opens does not vibrate, whichever it is`() {
        for (problem in TrackingProblem.entries) runTest(dispatcher) {
            problem(problem)
            val vm = viewModel()
            val shown = problemsOn(vm)
            advanceTimeBy(READ_MS + 1)

            assertEquals("the trip was read after it opened", 7L, vm.uiState.value.activeTrip?.tripDbId)
            assertEquals("$problem", setOf(problem), shown.toSet())
            assertEquals("$problem", 0, vibrations(shown))
        }
    }

    @Test fun `one that comes up once it has opened vibrates once`() = runTest(dispatcher) {
        val vm = viewModel()
        val shown = problemsOn(vm)
        advanceTimeBy(READ_MS + 1)

        problem(TrackingProblem.NO_NETWORK)
        runCurrent()

        assertEquals(listOf(TrackingProblem.NONE, TrackingProblem.NO_NETWORK), shown.distinct())
        assertEquals(1, vibrations(shown))
    }

    @Test fun `one that comes up before the trip has been read vibrates once`() = runTest(dispatcher) {
        val vm = viewModel()
        val shown = problemsOn(vm)

        problem(TrackingProblem.AUTH_EXPIRED)
        runCurrent()
        assertNull("the trip is still to be read", vm.uiState.value.activeTrip)
        advanceTimeBy(READ_MS + 1)

        assertEquals(TrackingProblem.AUTH_EXPIRED, vm.uiState.value.tracking.problem)
        assertEquals(1, vibrations(shown))
    }

    @Test fun `the first state has the map's file, so the map is drawn once`() = runTest(dispatcher) {
        mapFiles.state.value = MapFileState.Downloading(40, 100)

        assertEquals(MapFileState.Downloading(40, 100), viewModel().uiState.value.mapFile)
    }

    private companion object {
        const val READ_MS = 100L
    }
}
