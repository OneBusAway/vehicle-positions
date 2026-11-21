package org.onebusaway.vehicletracker.ui.runs

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.InkButton
import org.onebusaway.vehicletracker.ui.components.QuietButton
import org.onebusaway.vehicletracker.ui.isolated

/**
 * Asks before [run] starts, as iOS's run board does (`TripsView.swift`). Runs a few minutes apart all fall inside the
 * schedule's early and late window, so a wrong run would report against the wrong trip with nothing to catch it. The
 * title has the start time as the board shows it, and the line under it the route, where the run goes and the vehicle.
 */
@Composable
internal fun StartRunDialog(run: BoardRun, routeName: String, vehicleId: String, onStart: () -> Unit, onCancel: () -> Unit) {
    AlertDialog(
        onDismissRequest = onCancel,
        icon = { Icon(painterResource(R.drawable.ic_play_circle), contentDescription = null) },
        iconContentColor = MaterialTheme.colorScheme.onSurfaceVariant,
        title = { Text(stringResource(R.string.runs_start_title, run.time)) },
        text = { Text(startMessage(routeName, run.headsign, vehicleId)) },
        confirmButton = { InkButton(stringResource(R.string.runs_start_confirm), onClick = onStart) },
        dismissButton = { QuietButton(stringResource(R.string.runs_start_cancel), onClick = onCancel) },
    )
}

/** What the run is, leaving out a route name or a headsign the feed did not give, so the sentence has no gap. */
@Composable
private fun startMessage(routeName: String, headsign: String, vehicleId: String): String {
    val route = isolated(routeName)
    val towards = isolated(headsign)
    val vehicle = isolated(vehicleId)
    return when {
        routeName.isNotBlank() && headsign.isNotBlank() -> stringResource(R.string.runs_start_message, route, towards, vehicle)
        routeName.isNotBlank() -> stringResource(R.string.runs_start_message_no_headsign, route, vehicle)
        headsign.isNotBlank() -> stringResource(R.string.runs_start_message_unnamed, towards, vehicle)
        else -> stringResource(R.string.runs_start_message_vehicle, vehicle)
    }
}

/** The run [id] names, as the boards show it now: null when none is named, the runs are not loaded, or no board has it. */
internal fun RunsUiState.runNamed(id: String?): BoardRun? {
    if (id == null || this !is RunsUiState.Loaded) return null
    return boards.firstNotNullOfOrNull { board -> board.runs.firstOrNull { it.id == id } }
}
