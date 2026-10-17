package org.onebusaway.vehicletracker.ui.permissions

import android.os.Build

/**
 * What the phone has granted, read again at every step, and the Android version that decides what there is to ask:
 * background location is a permission of its own from Android 10, and notifications from Android 13.
 */
internal data class Grants(
    val sdk: Int,
    val fine: Boolean,
    val coarse: Boolean,
    val background: Boolean,
    val notifications: Boolean,
    val batteryExempt: Boolean,
) {
    /** Background location is asked for only once precise location is granted. */
    val needsBackground: Boolean get() = sdk >= Build.VERSION_CODES.Q && fine && !background

    val needsNotifications: Boolean get() = sdk >= Build.VERSION_CODES.TIRAMISU && !notifications
}

/** Where the sequence before a trip is: asking Android for something, explaining it first, or waiting on the driver. */
internal enum class PermissionStep {
    IDLE,
    REQUEST_LOCATION,
    APPROX_WARNING,
    LOCATION_DENIED,
    BACKGROUND_EXPLAIN,
    REQUEST_BACKGROUND,
    REQUEST_NOTIFICATIONS,
    NOTIFICATIONS_DENIED_INFO,
    BATTERY_EXPLAIN,
    REQUEST_BATTERY,
    CHECK_LOCATION_SETTINGS,
    LOCATION_SETTINGS_OFF,
    READY,
}

/** What happened at a step: Android answered, or the driver chose. */
internal sealed interface PermissionEvent {
    data object Begin : PermissionEvent

    /** Android's answer to fine and coarse location, asked together. [canAskAgain] is false once refused for good. */
    data class LocationAnswered(val fine: Boolean, val coarse: Boolean, val canAskAgain: Boolean) : PermissionEvent

    /** Ask Android for location again: for precise after only approximate, or after a refusal. */
    data object AskAgain : PermissionEvent
    data object ContinueApproximate : PermissionEvent

    /** Continue past an explanation, to Android's own prompt. */
    data object Explained : PermissionEvent
    data object NotNow : PermissionEvent
    data object BackgroundAnswered : PermissionEvent
    data class NotificationsAnswered(val granted: Boolean) : PermissionEvent
    data object Acknowledged : PermissionEvent
    data object BatteryAnswered : PermissionEvent
    data class LocationServicesChecked(val on: Boolean) : PermissionEvent
    data object Cancel : PermissionEvent

    /** The trip was handed on once everything was ready. */
    data object Started : PermissionEvent
}

internal data class PermissionFlowState(
    val step: PermissionStep = PermissionStep.IDLE,
    /** Location was refused and Android will not ask again, so only the app's settings page can grant it. */
    val locationDeniedForGood: Boolean = false,
)

/**
 * The sequence before a trip, one step at a time: location (the only refusal that stops it), the approximate-only
 * warning, background location, notifications, the battery exemption, then the phone's location services. [grants] is
 * what the phone has granted once [event] happened. An event that does not belong to the current step changes nothing.
 */
internal fun PermissionFlowState.next(event: PermissionEvent, grants: Grants): PermissionFlowState {
    // Tapping a run starts over from the top, wherever the last tap left off.
    if (event == PermissionEvent.Begin) return PermissionFlowState(PermissionStep.REQUEST_LOCATION)
    fun to(step: PermissionStep) = copy(step = step)
    return when (step) {
        PermissionStep.IDLE -> this
        PermissionStep.REQUEST_LOCATION -> when {
            event !is PermissionEvent.LocationAnswered -> this
            event.fine -> to(afterLocation(grants))
            event.coarse -> to(PermissionStep.APPROX_WARNING)
            else -> PermissionFlowState(PermissionStep.LOCATION_DENIED, locationDeniedForGood = !event.canAskAgain)
        }
        PermissionStep.APPROX_WARNING -> when (event) {
            PermissionEvent.AskAgain -> to(PermissionStep.REQUEST_LOCATION)
            PermissionEvent.ContinueApproximate -> to(afterLocation(grants))
            else -> this
        }
        PermissionStep.LOCATION_DENIED -> when (event) {
            PermissionEvent.AskAgain -> to(PermissionStep.REQUEST_LOCATION)
            PermissionEvent.Cancel -> PermissionFlowState()
            else -> this
        }
        PermissionStep.BACKGROUND_EXPLAIN -> when (event) {
            PermissionEvent.Explained -> to(PermissionStep.REQUEST_BACKGROUND)
            PermissionEvent.NotNow -> to(afterBackground(grants))
            else -> this
        }
        // Refusing background location does not stop the trip: the app only cannot restart reports by itself.
        PermissionStep.REQUEST_BACKGROUND -> if (event == PermissionEvent.BackgroundAnswered) to(afterBackground(grants)) else this
        PermissionStep.REQUEST_NOTIFICATIONS -> when {
            event !is PermissionEvent.NotificationsAnswered -> this
            event.granted -> to(afterNotifications(grants))
            else -> to(PermissionStep.NOTIFICATIONS_DENIED_INFO)
        }
        PermissionStep.NOTIFICATIONS_DENIED_INFO -> if (event == PermissionEvent.Acknowledged) to(afterNotifications(grants)) else this
        PermissionStep.BATTERY_EXPLAIN -> when (event) {
            PermissionEvent.Explained -> to(PermissionStep.REQUEST_BATTERY)
            PermissionEvent.NotNow -> to(PermissionStep.CHECK_LOCATION_SETTINGS)
            else -> this
        }
        // Whatever the answer: the exemption only makes reports steadier.
        PermissionStep.REQUEST_BATTERY -> if (event == PermissionEvent.BatteryAnswered) to(PermissionStep.CHECK_LOCATION_SETTINGS) else this
        PermissionStep.CHECK_LOCATION_SETTINGS -> when {
            event !is PermissionEvent.LocationServicesChecked -> this
            event.on -> to(PermissionStep.READY)
            else -> to(PermissionStep.LOCATION_SETTINGS_OFF)
        }
        PermissionStep.LOCATION_SETTINGS_OFF -> when (event) {
            PermissionEvent.AskAgain -> to(PermissionStep.CHECK_LOCATION_SETTINGS)
            PermissionEvent.Cancel -> PermissionFlowState()
            else -> this
        }
        PermissionStep.READY -> if (event == PermissionEvent.Started) PermissionFlowState() else this
    }
}

private fun afterLocation(grants: Grants) =
    if (grants.needsBackground) PermissionStep.BACKGROUND_EXPLAIN else afterBackground(grants)

private fun afterBackground(grants: Grants) =
    if (grants.needsNotifications) PermissionStep.REQUEST_NOTIFICATIONS else afterNotifications(grants)

private fun afterNotifications(grants: Grants) =
    if (grants.batteryExempt) PermissionStep.CHECK_LOCATION_SETTINGS else PermissionStep.BATTERY_EXPLAIN
