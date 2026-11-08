package org.onebusaway.vehicletracker.service

import android.app.ForegroundServiceStartNotAllowedException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class StartForegroundOrAskDriverTest {
    private var asked = 0

    private fun startForegroundThrowing(failure: RuntimeException?): Boolean =
        startForegroundOrAskDriver(
            startForeground = { failure?.let { throw it } },
            resumeNotifier = { asked++ },
        )

    @Test fun `a foreground start Android allows asks nobody`() {
        assertTrue(startForegroundThrowing(null))

        assertEquals(0, asked)
    }

    @Test fun `a restart refused while-using location asks the driver`() {
        // What startForeground threw on Android 17 after the revoke in #250.
        val refusal = SecurityException(
            "Starting FGS with type location callerApp=ProcessRecord{...} targetSDK=36 requires permissions: all of " +
                "the permissions [android.permission.FOREGROUND_SERVICE_LOCATION] and any of the permissions " +
                "[android.permission.ACCESS_COARSE_LOCATION, android.permission.ACCESS_FINE_LOCATION] and the app " +
                "must be in the eligible state/exemptions to access the foreground only permission",
        )

        assertFalse(startForegroundThrowing(refusal))

        assertEquals(1, asked)
    }

    @Test fun `a foreground start Android does not allow from the background asks the driver`() {
        val refusal = ForegroundServiceStartNotAllowedException(
            "startForeground() not allowed due to mAllowStartForeground false",
        )

        assertFalse(startForegroundThrowing(refusal))

        assertEquals(1, asked)
    }

    @Test fun `any other failure propagates and asks nobody`() {
        val failure = RuntimeException("not one of Android's refusals")

        val thrown = runCatching { startForegroundThrowing(failure) }.exceptionOrNull()

        assertSame(failure, thrown)
        assertEquals(0, asked)
    }
}
