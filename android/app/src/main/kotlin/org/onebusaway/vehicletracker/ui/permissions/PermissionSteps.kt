package org.onebusaway.vehicletracker.ui.permissions

import android.os.Build
import androidx.compose.runtime.Immutable

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

    fun allows(row: PermissionRow): Boolean = when (row) {
        PermissionRow.LOCATION -> fine || coarse
        PermissionRow.PRECISE -> fine
        // Before Android 10 location is not split into while in use and all the time: having it is having it all.
        PermissionRow.BACKGROUND -> if (sdk < Build.VERSION_CODES.Q) fine || coarse else background
        PermissionRow.NOTIFICATIONS -> sdk < Build.VERSION_CODES.TIRAMISU || notifications
        PermissionRow.BATTERY -> batteryExempt
        // Known only once the phone has been asked, on the way to a trip.
        PermissionRow.LOCATION_SERVICES -> false
    }

    /** Nothing for the checklist to ask about: the run can start without showing it, as it did before there was one. */
    val hasEverything: Boolean get() = PermissionRow.entries.all { it == PermissionRow.LOCATION_SERVICES || allows(it) }
}

/** Where the sequence before a trip is: asking Android for something, or waiting on the driver. */
internal enum class PermissionStep {
    IDLE,
    REQUEST_LOCATION,
    APPROX_WARNING,
    LOCATION_DENIED,
    OPEN_APP_SETTINGS,
    REQUEST_BACKGROUND,
    REQUEST_NOTIFICATIONS,
    REQUEST_BATTERY,
    CHECK_LOCATION_SETTINGS,
    LOCATION_SETTINGS_OFF,
    READY,
}

/** What happened at a step: Android answered, or the driver chose. */
internal sealed interface PermissionEvent {
    data object Begin : PermissionEvent

    /**
     * Android's answer to fine and coarse location, asked together, and whether Android would show a rationale for
     * them just before the ask ([rationaleBefore]) and just after it ([rationaleAfter]).
     */
    data class LocationAnswered(
        val fine: Boolean,
        val coarse: Boolean,
        val rationaleBefore: Boolean,
        val rationaleAfter: Boolean,
    ) : PermissionEvent

    /** Ask again: for precise location after only approximate, for location after a refusal, or the location services. */
    data object AskAgain : PermissionEvent
    data object ContinueApproximate : PermissionEvent

    /** The driver came back from the app's settings page, where location may now be allowed. */
    data object SettingsClosed : PermissionEvent
    data object BackgroundAnswered : PermissionEvent
    data object NotificationsAnswered : PermissionEvent
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
    /** The last ask for location ended with no rationale before or after it: dismissed, or refused at once. */
    val locationAskUnanswered: Boolean = false,
) {
    /** Stopped at a row, until the driver acts on it. */
    val waitsForDriver: Boolean
        get() = step == PermissionStep.LOCATION_DENIED || step == PermissionStep.APPROX_WARNING || step == PermissionStep.LOCATION_SETTINGS_OFF

    /** What Continue does here, or null when it has nothing to do: Android is asking, or a row must be acted on first. */
    val continueEvent: PermissionEvent?
        get() = when (step) {
            PermissionStep.IDLE -> PermissionEvent.Begin
            PermissionStep.APPROX_WARNING -> PermissionEvent.ContinueApproximate
            else -> null
        }
}

/**
 * The sequence before a trip, one step at a time: location (the only refusal that stops it), the approximate-only
 * choice, background location, notifications, the battery exemption, then the phone's location services. [grants] is
 * what the phone has granted once [event] happened. An event that does not belong to the current step changes nothing.
 */
internal fun PermissionFlowState.next(event: PermissionEvent, grants: Grants): PermissionFlowState {
    // Tapping a run starts over from the top, wherever the last tap left off; leaving the checklist ends it anywhere.
    if (event == PermissionEvent.Begin) return PermissionFlowState(PermissionStep.REQUEST_LOCATION)
    if (event == PermissionEvent.Cancel) return PermissionFlowState()
    fun to(step: PermissionStep) = copy(step = step)
    return when (step) {
        PermissionStep.IDLE -> this
        PermissionStep.REQUEST_LOCATION -> when {
            event !is PermissionEvent.LocationAnswered -> this
            event.fine -> to(afterLocation(grants))
            event.coarse -> to(PermissionStep.APPROX_WARNING)
            else -> refusedLocation(event)
        }
        PermissionStep.APPROX_WARNING -> when (event) {
            PermissionEvent.AskAgain -> to(PermissionStep.REQUEST_LOCATION)
            PermissionEvent.ContinueApproximate -> to(afterLocation(grants))
            else -> this
        }
        // Asking Android again after a refusal for good would only be refused again at once, so the driver is sent
        // to the one place it can still be changed.
        PermissionStep.LOCATION_DENIED -> when {
            event != PermissionEvent.AskAgain -> this
            locationDeniedForGood -> to(PermissionStep.OPEN_APP_SETTINGS)
            else -> to(PermissionStep.REQUEST_LOCATION)
        }
        // Android answers at once from what was set there, with no prompt.
        PermissionStep.OPEN_APP_SETTINGS -> if (event == PermissionEvent.SettingsClosed) to(PermissionStep.REQUEST_LOCATION) else this
        // Refusing background location does not stop the trip: the app only cannot restart reports by itself.
        PermissionStep.REQUEST_BACKGROUND -> if (event == PermissionEvent.BackgroundAnswered) to(afterBackground(grants)) else this
        PermissionStep.REQUEST_NOTIFICATIONS -> if (event == PermissionEvent.NotificationsAnswered) to(afterNotifications(grants)) else this
        // Whatever the answer: the exemption only makes reports steadier.
        PermissionStep.REQUEST_BATTERY -> if (event == PermissionEvent.BatteryAnswered) to(PermissionStep.CHECK_LOCATION_SETTINGS) else this
        PermissionStep.CHECK_LOCATION_SETTINGS -> when {
            event !is PermissionEvent.LocationServicesChecked -> this
            event.on -> to(PermissionStep.READY)
            else -> to(PermissionStep.LOCATION_SETTINGS_OFF)
        }
        PermissionStep.LOCATION_SETTINGS_OFF -> if (event == PermissionEvent.AskAgain) to(PermissionStep.CHECK_LOCATION_SETTINGS) else this
        PermissionStep.READY -> if (event == PermissionEvent.Started) PermissionFlowState() else this
    }
}

/**
 * Android shows a rationale after a first refusal, and stops once location is refused for good: a second refusal on
 * Android 11 and later, "Don't ask again" before. With no rationale before the ask or after it, the prompt was dismissed
 * without an answer, or Android refused at once because of a refusal for good earlier. Asking once more tells them
 * apart: a prompt that shows can be answered, and a second refusal at once is for good.
 */
private fun PermissionFlowState.refusedLocation(answer: PermissionEvent.LocationAnswered): PermissionFlowState {
    val unanswered = !answer.rationaleBefore && !answer.rationaleAfter
    return copy(
        step = PermissionStep.LOCATION_DENIED,
        locationDeniedForGood = !answer.rationaleAfter && (answer.rationaleBefore || locationAskUnanswered || locationDeniedForGood),
        locationAskUnanswered = unanswered,
    )
}

private fun afterLocation(grants: Grants) =
    if (grants.needsBackground) PermissionStep.REQUEST_BACKGROUND else afterBackground(grants)

private fun afterBackground(grants: Grants) =
    if (grants.needsNotifications) PermissionStep.REQUEST_NOTIFICATIONS else afterNotifications(grants)

private fun afterNotifications(grants: Grants) =
    if (grants.batteryExempt) PermissionStep.CHECK_LOCATION_SETTINGS else PermissionStep.REQUEST_BATTERY

/** The checklist's rows, in the order Android is asked. Location and precise location are asked about together. */
internal enum class PermissionRow { LOCATION, PRECISE, BACKGROUND, NOTIFICATIONS, BATTERY, LOCATION_SERVICES }

/** A row as the checklist shows it: not reached yet, the next one Android asks about, answered, or waiting on the driver. */
internal enum class RowState { NOT_REACHED, NEXT, ALLOWED, SKIPPED, NEEDED, NEEDED_IN_SETTINGS, APPROXIMATE, OFF }

@Immutable
internal data class ChecklistRow(val row: PermissionRow, val state: RowState)

private val PermissionStep.row: PermissionRow?
    get() = when (this) {
        PermissionStep.IDLE, PermissionStep.READY -> null
        PermissionStep.REQUEST_LOCATION, PermissionStep.LOCATION_DENIED, PermissionStep.OPEN_APP_SETTINGS -> PermissionRow.LOCATION
        PermissionStep.APPROX_WARNING -> PermissionRow.PRECISE
        PermissionStep.REQUEST_BACKGROUND -> PermissionRow.BACKGROUND
        PermissionStep.REQUEST_NOTIFICATIONS -> PermissionRow.NOTIFICATIONS
        PermissionStep.REQUEST_BATTERY -> PermissionRow.BATTERY
        PermissionStep.CHECK_LOCATION_SETTINGS, PermissionStep.LOCATION_SETTINGS_OFF -> PermissionRow.LOCATION_SERVICES
    }

/**
 * Every row, given where the sequence is. Rows already granted say so before Android is asked anything; a row the
 * sequence has passed without a grant was skipped; before Continue, the first row missing anything is next.
 */
internal fun PermissionFlowState.checklist(grants: Grants): List<ChecklistRow> {
    val current = when (step) {
        PermissionStep.IDLE -> PermissionRow.entries.first { !grants.allows(it) }
        else -> step.row
    }
    return PermissionRow.entries.map { row ->
        val state = when {
            current == null || row < current ->
                if (row == PermissionRow.LOCATION_SERVICES || grants.allows(row)) RowState.ALLOWED else RowState.SKIPPED
            row == current -> when (step) {
                PermissionStep.LOCATION_DENIED -> if (locationDeniedForGood) RowState.NEEDED_IN_SETTINGS else RowState.NEEDED
                PermissionStep.APPROX_WARNING -> RowState.APPROXIMATE
                PermissionStep.LOCATION_SETTINGS_OFF -> RowState.OFF
                else -> RowState.NEXT
            }
            grants.allows(row) -> RowState.ALLOWED
            else -> RowState.NOT_REACHED
        }
        ChecklistRow(row, state)
    }
}
