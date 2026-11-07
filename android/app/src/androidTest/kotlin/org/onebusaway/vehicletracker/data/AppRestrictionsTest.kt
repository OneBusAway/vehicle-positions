package org.onebusaway.vehicletracker.data

import android.content.RestrictionEntry
import android.content.RestrictionsManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Test
import org.junit.runner.RunWith

/**
 * What an agency's device management console offers to set is read from the installed app's manifest, which only a
 * device has: the key the app reads must be one the manifest declares, or the agency has nowhere to type the server.
 */
@RunWith(AndroidJUnit4::class)
class AppRestrictionsTest {
    @Test
    fun theManifestDeclaresTheServerAddressAsAString() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val restrictions = context.getSystemService(RestrictionsManager::class.java)

        val entries = restrictions.getManifestRestrictions(context.packageName)
        val server = entries.singleOrNull { it.key == MANAGED_SERVER_URL_KEY }

        assertNotNull("declared keys: ${entries.map { it.key }}", server)
        assertEquals(RestrictionEntry.TYPE_STRING, server!!.type)
    }
}
