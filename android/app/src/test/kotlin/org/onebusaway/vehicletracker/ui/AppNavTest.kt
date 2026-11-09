package org.onebusaway.vehicletracker.ui

import androidx.navigation.navOptions
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.ui.account.AccountUiState

class AppNavTest {
    @Test fun `the one-bus skip takes the bus step off the back stack`() {
        val options = navOptions(skipBusStep)
        assertEquals("vehicles", options.popUpToRoute)
        assertTrue("the bus step itself goes too, so Back leaves the flow", options.isPopUpToInclusive())
    }

    private val signedIn = AccountUiState(email = "d@example.com", server = "tracker.example.org")

    @Test fun `the vehicle step the app opened skips, and the one the driver asked for doesn't`() {
        assertTrue("opened by itself, after sign-in or a trip", skipsOnlyVehicle(ROUTE_VEHICLES, signedIn))
        assertFalse("opened by Change vehicle, so it shows the one vehicle", skipsOnlyVehicle(ROUTE_VEHICLES_CHOSEN, signedIn))
        assertFalse(skipsOnlyVehicle(null, signedIn))
    }

    @Test fun `the vehicle step doesn't skip once Sign out is confirmed`() {
        assertFalse("still waiting on the server", skipsOnlyVehicle(ROUTE_VEHICLES, signedIn.copy(signingOut = true)))
        assertFalse(
            "already forgotten, before Sign in opens",
            skipsOnlyVehicle(ROUTE_VEHICLES, signedIn.copy(signingOut = true, signedOut = true)),
        )
    }

    @Test fun `leaving for Sign in clears the whole flow`() {
        val options = navOptions(toSignIn)
        assertEquals(0, options.popUpToId)
        assertTrue("the first step goes too, so Back closes the app rather than reopening a picker", options.isPopUpToInclusive())
    }
}
