package org.onebusaway.vehicletracker.ui

import androidx.navigation.navOptions
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AppNavTest {
    @Test fun `the one-bus skip takes the bus step off the back stack`() {
        val options = navOptions(skipBusStep)
        assertEquals("vehicles", options.popUpToRoute)
        assertTrue("the bus step itself goes too, so Back leaves the flow", options.isPopUpToInclusive())
    }

    @Test fun `the vehicle step the app opened skips, and the one the driver asked for doesn't`() {
        assertTrue("opened by itself, after sign-in or a trip", skipsOnlyVehicle(ROUTE_VEHICLES))
        assertFalse("opened by Change vehicle, so it shows the one vehicle", skipsOnlyVehicle(ROUTE_VEHICLES_CHOSEN))
        assertFalse(skipsOnlyVehicle(null))
    }

    @Test fun `leaving for Sign in clears the whole flow`() {
        val options = navOptions(toSignIn)
        assertEquals(0, options.popUpToId)
        assertTrue("the first step goes too, so Back closes the app rather than reopening a picker", options.isPopUpToInclusive())
    }
}
