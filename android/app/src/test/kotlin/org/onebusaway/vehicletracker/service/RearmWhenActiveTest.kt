package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState

class RearmWhenActiveTest {
    private val state = MutableStateFlow(TrackingState())
    private var starts = 0

    private fun TestScope.startCollecting() {
        backgroundScope.launch { rearmWhenActive(state) { starts++ } }
        runCurrent()
    }

    /** Lets the collector see each value: a StateFlow keeps only the latest one. */
    private fun TestScope.emit(next: TrackingState) {
        state.value = next
        runCurrent()
    }

    @Test fun `already active when collection starts re-arms once`() = runTest {
        state.value = TrackingState(active = true)

        startCollecting()

        assertEquals(1, starts)
    }

    @Test fun `turning active while resumed re-arms`() = runTest {
        startCollecting()
        assertEquals(0, starts)

        emit(TrackingState(active = true))

        assertEquals(1, starts)
    }

    @Test fun `turning inactive does not restart the trip the driver ended`() = runTest {
        state.value = TrackingState(active = true)
        startCollecting()

        emit(TrackingState())

        assertEquals(1, starts)
    }

    @Test fun `a problem changing while active does not re-arm again`() = runTest {
        state.value = TrackingState(active = true)
        startCollecting()

        emit(state.value.copy(problem = TrackingProblem.NO_NETWORK))
        emit(state.value.copy(problem = TrackingProblem.NONE, fixesSent = 1))

        assertEquals(1, starts)
    }

    @Test fun `inactive throughout never starts the service`() = runTest {
        startCollecting()

        assertEquals(0, starts)
    }

    @Test fun `a trip started after the last one ended re-arms again`() = runTest {
        state.value = TrackingState(active = true)
        startCollecting()

        emit(TrackingState())
        emit(TrackingState(active = true))

        assertEquals(2, starts)
    }
}
