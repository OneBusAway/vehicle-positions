package org.onebusaway.vehicletracker.service

import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.FakeServiceController
import org.onebusaway.vehicletracker.data.FakeTripStateStore

class RestartTrackingTest {
    private val now = 1_794_067_200L
    private val trips = FakeTripStateStore()
    private val services = FakeServiceController()
    private var asked = 0
    private var background = true
    private var inFront = false
    private val restartTracking = RestartTracking(
        tripStateStore = trips,
        serviceController = services,
        resumeNotifier = { asked++ },
        clock = { now },
        canReportInBackground = { background },
        appInFront = { inFront },
    )

    private fun storeTripStarted(secondsAgo: Long) {
        trips.tripState.value = ActiveTrip(
            tripDbId = 42,
            gtfsTripId = "T1",
            vehicleId = "bus-1",
            routeId = "7",
            startDate = "20261107",
            startedAtEpochSec = now - secondsAgo,
            driverId = "7",
        )
    }

    @Test fun `with no stored trip nothing starts and nobody is asked`() = runTest {
        assertEquals(RestartOutcome.NoTrip, restartTracking.restart())

        assertEquals(0, services.startCount)
        assertEquals(0, asked)
    }

    @Test fun `a stored trip with background location starts tracking again`() = runTest {
        storeTripStarted(secondsAgo = 600)

        assertEquals(RestartOutcome.Started, restartTracking.restart())

        assertEquals(1, services.startCount)
        assertEquals(0, asked)
    }

    @Test fun `a start Android refuses asks the driver`() = runTest {
        storeTripStarted(secondsAgo = 600)
        services.startFailure = IllegalStateException("startForegroundService() not allowed")

        assertEquals(RestartOutcome.AskedDriver, restartTracking.restart())

        assertEquals(1, services.startCount)
        assertEquals(1, asked)
    }

    @Test fun `a start refused a permission asks the driver`() = runTest {
        storeTripStarted(secondsAgo = 600)
        services.startFailure = SecurityException("Starting FGS with type location requires permissions")

        assertEquals(RestartOutcome.AskedDriver, restartTracking.restart())

        assertEquals(1, asked)
    }

    @Test fun `any other failure propagates and asks nobody`() = runTest {
        storeTripStarted(secondsAgo = 600)
        val failure = RuntimeException("not one of Android's refusals")
        services.startFailure = failure

        val thrown = runCatching { restartTracking.restart() }.exceptionOrNull()

        assertSame(failure, thrown)
        assertEquals(0, asked)
    }

    @Test fun `a trip the driver ended is not restarted`() = runTest {
        storeTripStarted(secondsAgo = 600)
        trips.endActiveTripLocally(signedInDriverId = "7")

        assertEquals(RestartOutcome.NoTrip, restartTracking.restart())

        assertEquals(0, services.startCount)
        assertEquals(0, asked)
    }

    @Test fun `without background location the service is not started and the driver is asked`() = runTest {
        storeTripStarted(secondsAgo = 600)
        background = false

        assertEquals(RestartOutcome.AskedDriver, restartTracking.restart())

        assertEquals(0, services.startCount)
        assertEquals(1, asked)
    }

    @Test fun `with the app in front the trip is left to it, as after a force-stop`() = runTest {
        storeTripStarted(secondsAgo = 600)
        inFront = true

        assertEquals(RestartOutcome.AppInFront, restartTracking.restart())

        assertEquals(0, services.startCount)
        assertEquals(0, asked)
    }

    @Test fun `with the app in front and no background location nobody is asked over its Resume screen`() = runTest {
        storeTripStarted(secondsAgo = 600)
        background = false
        inFront = true

        assertEquals(RestartOutcome.AppInFront, restartTracking.restart())

        assertEquals(0, asked)
    }

    @Test fun `a trip one second short of the limit starts again`() = runTest {
        storeTripStarted(secondsAgo = MAX_RESTART_AGE.seconds - 1)

        assertEquals(RestartOutcome.Started, restartTracking.restart())

        assertEquals(1, services.startCount)
        assertEquals(0, asked)
    }

    @Test fun `a trip exactly at the limit asks the driver instead of starting`() = runTest {
        storeTripStarted(secondsAgo = MAX_RESTART_AGE.seconds)

        assertEquals(RestartOutcome.AskedDriver, restartTracking.restart())

        assertEquals(0, services.startCount)
        assertEquals(1, asked)
    }

    @Test fun `a trip left open overnight asks the driver instead of starting`() = runTest {
        storeTripStarted(secondsAgo = MAX_RESTART_AGE.seconds + 9 * 3600)

        assertEquals(RestartOutcome.AskedDriver, restartTracking.restart())

        assertEquals(0, services.startCount)
        assertEquals(1, asked)
    }

    @Test fun `before Android 10 any location grant is enough to report in the background`() {
        assertTrue(canReportInBackground(sdk = 28, fine = true, coarse = false, background = false))
        assertTrue(canReportInBackground(sdk = 28, fine = false, coarse = true, background = false))
        assertFalse(canReportInBackground(sdk = 28, fine = false, coarse = false, background = false))
    }

    @Test fun `from Android 10 only background location is enough`() {
        assertFalse(canReportInBackground(sdk = 29, fine = true, coarse = true, background = false))
        assertTrue(canReportInBackground(sdk = 29, fine = true, coarse = true, background = true))
        assertFalse(canReportInBackground(sdk = 34, fine = true, coarse = true, background = false))
        assertTrue(canReportInBackground(sdk = 34, fine = true, coarse = true, background = true))
        assertFalse(canReportInBackground(sdk = 34, fine = false, coarse = false, background = false))
    }
}
