package org.onebusaway.vehicletracker.data

import android.os.ParcelFileDescriptor
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.IOException

/**
 * Covers what the JVM cannot: a warning through [AppLog] still reaches logcat, where a developer with the phone on a
 * cable reads it, as Log.w did. The log is read through the shell, which may read it: the app itself would be asked
 * for "all device logs".
 */
@RunWith(AndroidJUnit4::class)
class AppLogTest {
    @Test
    fun aWarningStillReachesLogcat() {
        val marker = "AppLogTest ${System.nanoTime()}"

        AppLog.w(TAG, marker, IOException("disk full"))

        val log = shell("logcat -d -v brief -s $TAG:W")
        assertTrue(log, log.lines().any { it.startsWith("W/$TAG") && it.endsWith(marker) })
        assertTrue("with what was thrown:\n$log", log.contains("java.io.IOException: disk full"))
    }

    private fun shell(command: String): String {
        val output = InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command)
        return ParcelFileDescriptor.AutoCloseInputStream(output).bufferedReader().use { it.readText() }
    }

    private companion object {
        const val TAG = "AppLogTest"
    }
}
