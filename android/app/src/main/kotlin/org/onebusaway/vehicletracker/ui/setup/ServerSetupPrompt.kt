package org.onebusaway.vehicletracker.ui.setup

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ServerSetup
import org.onebusaway.vehicletracker.data.ServerSetupLink.Decision
import org.onebusaway.vehicletracker.ui.components.InkButton
import org.onebusaway.vehicletracker.ui.components.QuietButton

/** The dialog a setup link leads to, over whichever screen is showing: a link can arrive on any of them. */
@Composable
fun ServerSetupPrompt(serverSetup: ServerSetup) {
    val decision by serverSetup.prompt.collectAsStateWithLifecycle()
    decision?.let { ServerSetupDialog(it, onConfirm = serverSetup::confirm, onDismiss = serverSetup::dismiss) }
}

/** Asks before a link's server fills in Sign in's field, or says why the link was not used. */
@Composable
fun ServerSetupDialog(decision: Decision, onConfirm: (server: String) -> Unit, onDismiss: () -> Unit) {
    when (decision) {
        is Decision.Confirm -> AlertDialog(
            onDismissRequest = onDismiss,
            title = { Text(stringResource(R.string.server_setup_confirm_title, decision.host)) },
            text = { Text(stringResource(R.string.server_setup_confirm_message)) },
            confirmButton = { InkButton(stringResource(R.string.server_setup_use), onClick = { onConfirm(decision.server) }) },
            dismissButton = { QuietButton(stringResource(R.string.server_setup_cancel), onClick = onDismiss) },
        )
        is Decision.RefusedSignedIn -> NotUsed(
            if (decision.host != null) {
                stringResource(R.string.server_setup_refused_signed_in, decision.host)
            } else {
                stringResource(R.string.server_setup_refused_trip_open)
            },
            onDismiss,
        )
        Decision.RefusedManaged -> NotUsed(stringResource(R.string.server_setup_refused_managed), onDismiss)
        Decision.Invalid -> NotUsed(stringResource(R.string.server_setup_invalid), onDismiss)
    }
}

/** Why the link was not used, with nothing to choose. */
@Composable
private fun NotUsed(message: String, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        text = { Text(message) },
        confirmButton = { QuietButton(stringResource(R.string.server_setup_ok), onClick = onDismiss) },
    )
}
