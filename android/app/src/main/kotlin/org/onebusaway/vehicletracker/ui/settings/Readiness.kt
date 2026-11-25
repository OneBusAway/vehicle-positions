package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.runtime.Immutable
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow

/** What the phone lets the app do right now: the checklist's grants, and whether location is on for the whole phone. */
internal data class PhoneAccess(val grants: Grants, val locationServicesOn: Boolean) {
    fun allows(row: PermissionRow): Boolean =
        if (row == PermissionRow.LOCATION_SERVICES) locationServicesOn else grants.allows(row)
}

/** What Settings says about this phone reporting a trip. */
internal sealed interface ReadinessState {
    /** No location at all for the app, which stops a trip. */
    data object LocationRefused : ReadinessState

    /** Location is off for the whole phone, which stops a trip. */
    data object LocationOff : ReadinessState

    /** A trip can start. [toFix] are the checks it can do without, in the checklist's order. */
    data class Ready(val toFix: List<PermissionRow>, val waiting: ReportsWaiting?) : ReadinessState
}

/** How a check's stop is drawn on the line. */
internal enum class StopState { ALLOWED, MISSING, BEYOND }

/**
 * The header of Settings: what it says, and the line under it. The line has a stop for each of the checklist's rows, in
 * its order, between the phone and the riders.
 */
@Immutable
internal data class Readiness(val state: ReadinessState, val stops: List<StopState>) {
    val canReport: Boolean get() = state is ReadinessState.Ready

    /**
     * Where the vehicle stands: 0 before the first stop, n on the nth, and one past the last stop by the riders. It gets
     * past a check a trip can do without, and waits on the stop before one it can't.
     */
    val vehicleAt: Int
        get() = when (state) {
            ReadinessState.LocationRefused -> PermissionRow.LOCATION.ordinal
            ReadinessState.LocationOff -> PermissionRow.LOCATION_SERVICES.ordinal
            is ReadinessState.Ready -> stops.size + 1
        }
}

/**
 * What stops a trip is what stops the checklist: no location for the app, or none on the phone. Approximate location
 * does not, so it is one more thing to fix.
 */
internal fun readiness(access: PhoneAccess, waiting: ReportsWaiting?): Readiness {
    val missing = PermissionRow.entries.filterNot(access::allows)
    val state = when {
        PermissionRow.LOCATION in missing -> ReadinessState.LocationRefused
        PermissionRow.LOCATION_SERVICES in missing -> ReadinessState.LocationOff
        else -> ReadinessState.Ready(missing, waiting)
    }
    val stops = PermissionRow.entries.map { row ->
        when {
            row !in missing -> StopState.ALLOWED
            // Android gives these only with location, so there is nothing to fix there yet.
            state == ReadinessState.LocationRefused && (row == PermissionRow.PRECISE || row == PermissionRow.BACKGROUND) -> StopState.BEYOND
            else -> StopState.MISSING
        }
    }
    return Readiness(state, stops)
}
