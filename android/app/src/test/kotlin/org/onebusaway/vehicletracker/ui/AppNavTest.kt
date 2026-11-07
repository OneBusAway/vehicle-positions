package org.onebusaway.vehicletracker.ui

import androidx.navigation.navOptions
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AppNavTest {
    @Test fun `the one-bus skip takes the bus step off the back stack`() {
        val options = navOptions(skipBusStep)
        assertEquals("vehicles", options.popUpToRoute)
        assertTrue("the bus step itself goes too, so Back leaves the flow", options.isPopUpToInclusive())
    }

    @Test fun `leaving for Sign in clears the whole flow`() {
        val options = navOptions(toSignIn)
        assertEquals(0, options.popUpToId)
        assertTrue("the first step goes too, so Back closes the app rather than reopening a picker", options.isPopUpToInclusive())
    }
}
