package org.onebusaway.vehicletracker.service

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Pins the manifest wiring the JVM cannot see: Android only delivers a reboot to an app that asks for
 * RECEIVE_BOOT_COMPLETED, and each broadcast only to a receiver whose filter names it.
 */
@RunWith(AndroidJUnit4::class)
class RestartTrackingReceiverTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    private fun receiversOf(action: String): List<String> =
        context.packageManager.queryBroadcastReceivers(Intent(action).setPackage(context.packageName), 0)
            .map { it.activityInfo.name }

    @Test
    fun aRebootReachesTheReceiver() {
        assertEquals(listOf(RestartTrackingReceiver::class.java.name), receiversOf(Intent.ACTION_BOOT_COMPLETED))
    }

    @Test
    fun anUpdateOfTheAppReachesTheReceiver() {
        assertEquals(listOf(RestartTrackingReceiver::class.java.name), receiversOf(Intent.ACTION_MY_PACKAGE_REPLACED))
    }

    @Test
    fun theAppAsksToHearOfReboots() {
        val requested = context.packageManager
            .getPackageInfo(context.packageName, PackageManager.GET_PERMISSIONS)
            .requestedPermissions.orEmpty()

        assertTrue(requested.joinToString(), Manifest.permission.RECEIVE_BOOT_COMPLETED in requested)
    }
}
