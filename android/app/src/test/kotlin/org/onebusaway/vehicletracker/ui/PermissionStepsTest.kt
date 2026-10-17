package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.Acknowledged
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.AskAgain
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.BackgroundAnswered
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.BatteryAnswered
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.Begin
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.Cancel
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.ContinueApproximate
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.Explained
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.LocationAnswered
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.LocationServicesChecked
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.NotNow
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.NotificationsAnswered
import org.onebusaway.vehicletracker.ui.permissions.PermissionEvent.Started
import org.onebusaway.vehicletracker.ui.permissions.PermissionFlowState
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.APPROX_WARNING
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.BACKGROUND_EXPLAIN
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.BATTERY_EXPLAIN
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.CHECK_LOCATION_SETTINGS
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.IDLE
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.LOCATION_DENIED
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.LOCATION_SETTINGS_OFF
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.NOTIFICATIONS_DENIED_INFO
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.READY
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.REQUEST_BACKGROUND
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.REQUEST_BATTERY
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.REQUEST_LOCATION
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep.REQUEST_NOTIFICATIONS
import org.onebusaway.vehicletracker.ui.permissions.next

/**
 * The permission sequence before a trip, as it has run since the app first asked: these describe today's flow, so
 * moving it into [next] changed nothing a driver sees.
 */
class PermissionStepsTest {
    private val nothingYet = Grants(sdk = 36, fine = false, coarse = false, background = false, notifications = false, batteryExempt = false)
    private val everything = Grants(sdk = 36, fine = true, coarse = true, background = true, notifications = true, batteryExempt = true)

    /** Runs [events] from IDLE, each answered with the grants beside it, and returns every step it passed through. */
    private fun walk(vararg events: Pair<PermissionEvent, Grants>): List<PermissionStep> {
        var state = PermissionFlowState()
        return events.map { (event, grants) ->
            state = state.next(event, grants)
            state.step
        }
    }

    private val fineAndCoarse = LocationAnswered(fine = true, coarse = true, canAskAgain = true)

    @Test fun `everything granted goes from location straight to the location services check`() {
        assertEquals(
            listOf(REQUEST_LOCATION, CHECK_LOCATION_SETTINGS, READY, IDLE),
            walk(Begin to everything, fineAndCoarse to everything, LocationServicesChecked(on = true) to everything, Started to everything),
        )
    }

    @Test fun `a first trip on Android 16 asks for each in turn`() {
        val fine = nothingYet.copy(fine = true, coarse = true)
        val background = fine.copy(background = true)
        val notified = background.copy(notifications = true)
        assertEquals(
            listOf(
                REQUEST_LOCATION, BACKGROUND_EXPLAIN, REQUEST_BACKGROUND, REQUEST_NOTIFICATIONS, BATTERY_EXPLAIN,
                REQUEST_BATTERY, CHECK_LOCATION_SETTINGS, READY,
            ),
            walk(
                Begin to nothingYet,
                fineAndCoarse to fine,
                Explained to fine,
                BackgroundAnswered to background,
                NotificationsAnswered(granted = true) to notified,
                Explained to notified,
                BatteryAnswered to notified.copy(batteryExempt = true),
                LocationServicesChecked(on = true) to everything,
            ),
        )
    }

    @Test fun `approximate only warns, and asking again goes back to Android`() {
        val coarse = nothingYet.copy(coarse = true)
        assertEquals(
            listOf(REQUEST_LOCATION, APPROX_WARNING, REQUEST_LOCATION, BACKGROUND_EXPLAIN),
            walk(
                Begin to nothingYet,
                LocationAnswered(fine = false, coarse = true, canAskAgain = true) to coarse,
                AskAgain to coarse,
                fineAndCoarse to coarse.copy(fine = true),
            ),
        )
    }

    @Test fun `going on with approximate location never asks for background location`() {
        val coarse = nothingYet.copy(coarse = true)
        assertEquals(
            listOf(REQUEST_LOCATION, APPROX_WARNING, REQUEST_NOTIFICATIONS),
            walk(
                Begin to nothingYet,
                LocationAnswered(fine = false, coarse = true, canAskAgain = true) to coarse,
                ContinueApproximate to coarse,
            ),
        )
    }

    @Test fun `background location can be put off, or refused, and the trip still starts`() {
        val fine = everything.copy(background = false)
        assertEquals(
            listOf(REQUEST_LOCATION, BACKGROUND_EXPLAIN, CHECK_LOCATION_SETTINGS),
            walk(Begin to fine, fineAndCoarse to fine, NotNow to fine),
        )
        assertEquals(
            listOf(REQUEST_LOCATION, BACKGROUND_EXPLAIN, REQUEST_BACKGROUND, CHECK_LOCATION_SETTINGS),
            walk(Begin to fine, fineAndCoarse to fine, Explained to fine, BackgroundAnswered to fine),
        )
    }

    @Test fun `refused notifications are explained once, then the battery is asked about`() {
        val noNotifications = everything.copy(notifications = false, batteryExempt = false)
        assertEquals(
            listOf(REQUEST_LOCATION, REQUEST_NOTIFICATIONS, NOTIFICATIONS_DENIED_INFO, BATTERY_EXPLAIN),
            walk(
                Begin to noNotifications,
                fineAndCoarse to noNotifications,
                NotificationsAnswered(granted = false) to noNotifications,
                Acknowledged to noNotifications,
            ),
        )
    }

    @Test fun `the battery exemption can be put off, or declined, and the trip still starts`() {
        val noExemption = everything.copy(batteryExempt = false)
        assertEquals(
            listOf(REQUEST_LOCATION, BATTERY_EXPLAIN, CHECK_LOCATION_SETTINGS),
            walk(Begin to noExemption, fineAndCoarse to noExemption, NotNow to noExemption),
        )
        assertEquals(
            listOf(REQUEST_LOCATION, BATTERY_EXPLAIN, REQUEST_BATTERY, CHECK_LOCATION_SETTINGS),
            walk(Begin to noExemption, fineAndCoarse to noExemption, Explained to noExemption, BatteryAnswered to noExemption),
        )
    }

    @Test fun `location services off waits for the driver, and checks again once they are on`() {
        assertEquals(
            listOf(REQUEST_LOCATION, CHECK_LOCATION_SETTINGS, LOCATION_SETTINGS_OFF, CHECK_LOCATION_SETTINGS, READY),
            walk(
                Begin to everything,
                fineAndCoarse to everything,
                LocationServicesChecked(on = false) to everything,
                AskAgain to everything,
                LocationServicesChecked(on = true) to everything,
            ),
        )
        assertEquals(
            listOf(REQUEST_LOCATION, CHECK_LOCATION_SETTINGS, LOCATION_SETTINGS_OFF, IDLE),
            walk(Begin to everything, fineAndCoarse to everything, LocationServicesChecked(on = false) to everything, Cancel to everything),
        )
    }

    @Test fun `refused location stops the trip, and a second refusal is for good`() {
        val refused = LocationAnswered(fine = false, coarse = false, canAskAgain = true)
        val refusedForGood = LocationAnswered(fine = false, coarse = false, canAskAgain = false)

        var state = PermissionFlowState().next(Begin, nothingYet).next(refused, nothingYet)
        assertEquals(LOCATION_DENIED, state.step)
        assertFalse(state.locationDeniedForGood)

        state = state.next(AskAgain, nothingYet)
        assertEquals(REQUEST_LOCATION, state.step)
        state = state.next(refusedForGood, nothingYet)
        assertEquals(LOCATION_DENIED, state.step)
        assertTrue(state.locationDeniedForGood)

        // Going on is not one of the choices: only asking again, or leaving the run.
        assertEquals(state, state.next(ContinueApproximate, nothingYet))
        assertEquals(PermissionFlowState(), state.next(Cancel, nothingYet))
        // A later run starts afresh, as Android may have been changed in settings meanwhile.
        assertEquals(PermissionFlowState(REQUEST_LOCATION), state.next(Begin, nothingYet))
    }

    @Test fun `Android 8 has no background location or notification permission to ask for`() {
        val oreo = nothingYet.copy(sdk = 26, fine = true, coarse = true, batteryExempt = true)
        assertEquals(listOf(REQUEST_LOCATION, CHECK_LOCATION_SETTINGS), walk(Begin to oreo, fineAndCoarse to oreo))
    }

    @Test fun `Android 10 asks for background location but not notifications`() {
        val q = nothingYet.copy(sdk = 29, fine = true, coarse = true, batteryExempt = true)
        assertEquals(
            listOf(REQUEST_LOCATION, BACKGROUND_EXPLAIN, REQUEST_BACKGROUND, CHECK_LOCATION_SETTINGS),
            walk(Begin to q, fineAndCoarse to q, Explained to q, BackgroundAnswered to q.copy(background = true)),
        )
    }

    @Test fun `Android 13 and 16 ask for notifications too`() {
        for (sdk in listOf(33, 36)) {
            val granted = everything.copy(sdk = sdk, notifications = false)
            assertEquals(
                "on API $sdk",
                listOf(REQUEST_LOCATION, REQUEST_NOTIFICATIONS, CHECK_LOCATION_SETTINGS),
                walk(Begin to granted, fineAndCoarse to granted, NotificationsAnswered(granted = true) to everything.copy(sdk = sdk)),
            )
        }
    }

    @Test fun `an answer that does not belong to the step changes nothing`() {
        val waiting = PermissionFlowState(REQUEST_NOTIFICATIONS)
        for (event in listOf(fineAndCoarse, AskAgain, Explained, NotNow, BackgroundAnswered, BatteryAnswered, LocationServicesChecked(true), Cancel, Started)) {
            assertEquals("after $event", waiting, waiting.next(event, everything))
        }
        assertEquals(PermissionFlowState(), PermissionFlowState().next(LocationServicesChecked(true), everything))
    }
}
