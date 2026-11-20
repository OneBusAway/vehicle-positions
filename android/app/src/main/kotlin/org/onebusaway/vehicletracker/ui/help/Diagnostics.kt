package org.onebusaway.vehicletracker.ui.help

import org.onebusaway.vehicletracker.data.RecentProblem
import org.onebusaway.vehicletracker.data.RecentProblems
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.ui.login.serverLabel
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

// What "Share diagnostics" sends: the app, the phone, the server, what the app may do and its recent problems, for
// whoever looks after the app. It stays in English whatever the phone's language, so that person can read it, and its
// dates and numbers are written the same way in every language (Locale.ROOT), so it can be searched and compared.

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

/**
 * The text the share sheet sends. Of the [session] it names only the server's host: no email and no token. The
 * [problems] are the app's warnings since it started, oldest first, already scrubbed, each stamped in [zone].
 */
internal fun diagnosticsText(device: DeviceFacts, session: Session, problems: List<RecentProblem>, zone: ZoneId): String = buildString {
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
    appendLine(rows.joinToString("\n"))
    appendLine("Recent problems")
    append(if (problems.isEmpty()) "None since the app started" else problems.joinToString("\n") { problemLine(it, zone) })
}

private val PROBLEM_AT = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss xxx", Locale.ROOT)

/** "2026-11-20 10:00:05 +05:30 HistoryUploader: Keeping queued fixes after network failure (IOException: timeout)" */
private fun problemLine(problem: RecentProblem, zone: ZoneId): String =
    RecentProblems.cut("${PROBLEM_AT.format(Instant.ofEpochMilli(problem.atEpochMs).atZone(zone))} ${problem.tag}: ${problem.message}")

private fun permissionLabel(row: PermissionRow): String = when (row) {
    PermissionRow.LOCATION -> "Location"
    PermissionRow.PRECISE -> "Precise location"
    PermissionRow.BACKGROUND -> "All the time"
    PermissionRow.NOTIFICATIONS -> "Notifications"
    PermissionRow.BATTERY -> "Battery"
    PermissionRow.LOCATION_SERVICES -> "Location services"
}
