package org.onebusaway.vehicletracker.service

import android.content.ContextWrapper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripGeometryStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.FakeVehiclePrefsStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.signedInAs
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import java.time.ZoneOffset

/**
 * "Reports paused" is posted for a trip with no service running, after a reboot or an update, and the driver ends that
 * trip from Resume. These end it through the real [ServiceControllerImpl], so the notification goes with the trip.
 */
class ServiceControllerImplTest {
    private val dispatcher = StandardTestDispatcher()
    private val server = MockWebServer()
    private val trip = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L, driverId = "2")
    private val tripState = FakeTripStateStore().apply { tripState.value = trip }
    private val geometryStore = FakeTripGeometryStore()
    private var cancelled = 0
    private var posted = 0

    // Android's own classes return defaults here, so the stop intent is built and sent to no one.
    private val serviceController =
        ServiceControllerImpl(ContextWrapper(null), cancelTripNotifications = { cancelled++ }, postResume = { posted++ })

    @Before fun setUp() {
        Dispatchers.setMain(dispatcher)
        server.start()
    }

    @After fun tearDown() {
        server.shutdown()
        Dispatchers.resetMain()
    }

    /** Resume, once it shows the stored trip. */
    private suspend fun resume(): ResumeShiftViewModel {
        val provider = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url("/").toString()) }
        val tripRepository = TripRepository(provider, tripState, FakeVehiclePrefsStore(), FakeSessionStore(), clock = { 0L })
        val tripEnder = TripEnder(tripRepository, tripState, geometryStore, serviceController, signedInAs("2"))
        val vm = ResumeShiftViewModel(tripState, geometryStore, FakeSessionStore(), serviceController, tripEnder, clock = { 0L }, phoneZone = ZoneOffset.UTC)
        vm.uiState.first { it.trip == trip }
        return vm
    }

    @Test fun `asking the driver to resume posts Reports paused`() {
        serviceController.askToResume()

        assertEquals(1, posted)
        assertEquals(0, cancelled)
    }

    @Test fun `ending the trip from Resume takes Reports paused down`() = runTest(dispatcher) {
        server.enqueue(MockResponse().setBody("""{"status":"trip ended"}"""))
        val vm = resume()
        val ended = CompletableDeferred<Unit>()

        vm.onEndShift { ended.complete(Unit) }
        ended.await()

        assertEquals(1, cancelled)
        assertEquals("/api/v1/trips/end", server.takeRequest().path)
    }

    @Test fun `ending it on this phone from Resume takes it down too`() = runTest(dispatcher) {
        val vm = resume()
        val ended = CompletableDeferred<Unit>()

        vm.onEndShiftLocally { ended.complete(Unit) }
        ended.await()

        assertEquals(1, cancelled)
        assertEquals(0, server.requestCount)
    }

    @Test fun `an end the server refuses leaves it up, as the trip is still open`() = runTest(dispatcher) {
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"internal error"}"""))
        val vm = resume()

        vm.onEndShift { error("a refused end must not leave Resume") }
        vm.uiState.first { it.endTripError }

        assertEquals(1, server.requestCount)
        assertEquals(0, cancelled)
        assertNotNull(tripState.tripState.value)
    }
}
