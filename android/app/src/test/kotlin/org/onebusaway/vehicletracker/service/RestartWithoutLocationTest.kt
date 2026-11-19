package org.onebusaway.vehicletracker.service

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RestartWithoutLocationTest {
    private fun restart(
        sdk: Int = 32,
        startedAgainByAndroid: Boolean = true,
        locationAllowed: Boolean = false,
    ): Boolean = restartWithoutLocation(sdk, startedAgainByAndroid, locationAllowed)

    @Test fun `Android 11 to 13 restarting without location asks the driver`() {
        // What Android 12L did after the revoke in #296: foreground status, no location, "Reporting".
        assertTrue(restart(sdk = 30))
        assertTrue(restart(sdk = 32))
        assertTrue(restart(sdk = 33))
    }

    @Test fun `a restart Android lets keep location reports`() {
        // A trip last started in the app: Android 12L kept location for the restart after the same revoke.
        assertFalse(restart(locationAllowed = true))
    }

    @Test fun `from Android 14 the refused startForeground asks instead`() {
        assertFalse(restart(sdk = 34))
        assertFalse(restart(sdk = 36))
    }

    @Test fun `before Android 11 a restart reports as before`() {
        assertFalse(restart(sdk = 29))
        assertFalse(restart(sdk = 26))
    }

    @Test fun `a start from the app reports`() {
        assertFalse(restart(startedAgainByAndroid = false))
    }
}
