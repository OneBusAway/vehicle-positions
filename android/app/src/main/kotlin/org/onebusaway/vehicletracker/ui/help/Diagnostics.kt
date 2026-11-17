package org.onebusaway.vehicletracker.ui.help

import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.ui.login.serverLabel
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow

// What "Share diagnostics" sends: the app, the phone, the server and what the app may do, for whoever looks after
// the app. It stays in English whatever the phone's language, so that person can read it.

/** What the phone says about itself and this build. Nothing in it names the driver, the vehicle or where it is. */
internal data class DeviceFacts(
    val madeAt: String,
    val version: String,
    val androidRelease: String,
    val sdk: Int,
    val phone: String,
    val grants: Grants,
    /** Whether the app's notifications are on, which a driver can turn off on any Android version. */
    val notificationsOn: Boolean,
    val locationServicesOn: Boolean,
)

/** The text the share sheet sends. Of the [session] it names only the server's host: no email and no token. */
internal fun diagnosticsText(device: DeviceFacts, session: Session): String = buildString {
    appendLine("Vehicle Tracker diagnostics")
    appendLine("Made: ${device.madeAt}")
    appendLine("App: ${device.version}")
    appendLine("Android: ${device.androidRelease} (API ${device.sdk})")
    appendLine("Phone: ${device.phone}")
    appendLine("Server: ${session.serverUrl?.let(::serverLabel) ?: "none"}")
    // The checklist's rows, under its names.
    val rows = PermissionRow.entries.map { row ->
        val state = when (row) {
            // The checklist counts notifications as allowed below Android 13, where there is nothing to ask for.
            PermissionRow.NOTIFICATIONS -> if (device.notificationsOn) "allowed" else "not allowed"
            PermissionRow.LOCATION_SERVICES -> if (device.locationServicesOn) "on" else "off"
            else -> if (device.grants.allows(row)) "allowed" else "not allowed"
        }
        "${permissionLabel(row)}: $state"
    }
    append(rows.joinToString("\n"))
}

private fun permissionLabel(row: PermissionRow): String = when (row) {
    PermissionRow.LOCATION -> "Location"
    PermissionRow.PRECISE -> "Precise location"
    PermissionRow.BACKGROUND -> "All the time"
    PermissionRow.NOTIFICATIONS -> "Notifications"
    PermissionRow.BATTERY -> "Battery"
    PermissionRow.LOCATION_SERVICES -> "Location services"
}
