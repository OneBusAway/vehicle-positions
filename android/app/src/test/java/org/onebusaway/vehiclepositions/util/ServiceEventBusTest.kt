package org.onebusaway.vehiclepositions.util

import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Before
import org.junit.Test

class ServiceEventBusTest {

    private lateinit var bus: ServiceEventBus

    @Before
    fun setup() {
        bus = ServiceEventBus()
    }

    @Test
    fun `ServiceEventBus initializes without error`() {
        assertNotNull(bus)
    }

    @Test
    fun `emitting StopShift event is received by collector`() = runTest {
        val events = mutableListOf<ServiceEvent>()

        // starts listening in the background before emitting
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            bus.events.toList(events)
        }

        bus.emitStopShift()

        assertEquals(ServiceEvent.StopShift, events.first())
    }

    @Test
    fun `emitting NavigateToLogin event is received by collector`() = runTest {
        val events = mutableListOf<ServiceEvent>()

        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            bus.events.toList(events)
        }

        bus.emitNavigateToLogin()

        assertEquals(ServiceEvent.NavigateToLogin, events.first())
    }

    @Test
    fun `emitting LocationPermissionRevoked event is received by collector`() = runTest {
        val events = mutableListOf<ServiceEvent>()

        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            bus.events.toList(events)
        }

        bus.emitLocationPermissionRevoked()

        assertEquals(ServiceEvent.LocationPermissionRevoked, events.first())
    }

    @Test
    fun `multiple events are received in order`() = runTest {
        val events = mutableListOf<ServiceEvent>()

        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            bus.events.toList(events)
        }

        bus.emitStopShift()
        bus.emitNavigateToLogin()
        bus.emitLocationPermissionRevoked()

        assertEquals(3, events.size)
        assertEquals(ServiceEvent.StopShift, events[0])
        assertEquals(ServiceEvent.NavigateToLogin, events[1])
        assertEquals(ServiceEvent.LocationPermissionRevoked, events[2])
    }
}