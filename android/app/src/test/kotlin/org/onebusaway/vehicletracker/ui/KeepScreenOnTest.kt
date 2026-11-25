package org.onebusaway.vehicletracker.ui

import android.os.BatteryManager
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.ScreenOn
import org.onebusaway.vehicletracker.ui.tracking.keepsScreenOn

class KeepScreenOnTest {
    private val onBattery = 0

    @Test fun `Tracking on a charger keeps the screen on, whatever the charger`() {
        val chargers = listOf(
            BatteryManager.BATTERY_PLUGGED_AC,
            BatteryManager.BATTERY_PLUGGED_USB,
            BatteryManager.BATTERY_PLUGGED_WIRELESS,
        )
        for (plugged in chargers) {
            assertTrue("plugged $plugged", keepsScreenOn(trackingShowing = true, plugged = plugged, ScreenOn.CHARGING))
        }
    }

    @Test fun `Tracking on battery lets the screen time out`() {
        assertFalse(keepsScreenOn(trackingShowing = true, plugged = onBattery, ScreenOn.CHARGING))
    }

    @Test fun `on a charger with Tracking gone or in the background, the screen times out`() {
        assertFalse(keepsScreenOn(trackingShowing = false, plugged = BatteryManager.BATTERY_PLUGGED_AC, ScreenOn.CHARGING))
    }

    @Test fun `on battery with Tracking gone, the screen times out`() {
        assertFalse(keepsScreenOn(trackingShowing = false, plugged = onBattery, ScreenOn.CHARGING))
    }

    @Test fun `chosen always on, Tracking keeps the screen on with or without a charger`() {
        for (plugged in listOf(onBattery, BatteryManager.BATTERY_PLUGGED_USB)) {
            assertTrue("plugged $plugged", keepsScreenOn(trackingShowing = true, plugged = plugged, ScreenOn.ALWAYS))
        }
    }

    @Test fun `chosen always on, the screen still times out with Tracking gone`() {
        for (plugged in listOf(onBattery, BatteryManager.BATTERY_PLUGGED_USB)) {
            assertFalse("plugged $plugged", keepsScreenOn(trackingShowing = false, plugged = plugged, ScreenOn.ALWAYS))
        }
    }
}
