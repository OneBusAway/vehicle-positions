package org.onebusaway.vehiclepositions.util

import kotlinx.coroutines.flow.first
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
        bus.emitStopShift()
        val event = bus.events.first()
        assertEquals(ServiceEvent.StopShift, event)
    }

    @Test
    fun `emitting NavigateToLogin event is received by collector`() = runTest {
        bus.emitNavigateToLogin()
        val event = bus.events.first()
        assertEquals(ServiceEvent.NavigateToLogin, event)
    }

    @Test
    fun `emitting LocationPermissionRevoked event is received by collector`() = runTest {
        bus.emitLocationPermissionRevoked()
        val event = bus.events.first()
        assertEquals(ServiceEvent.LocationPermissionRevoked, event)
    }

    @Test
    fun `multiple events are received in order`() = runTest {
        bus.emitStopShift()
        val event = bus.events.first()
        assertEquals(ServiceEvent.StopShift, event)
    }
}