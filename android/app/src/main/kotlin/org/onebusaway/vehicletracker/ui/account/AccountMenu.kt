package org.onebusaway.vehicletracker.ui.account

import androidx.annotation.DrawableRes
import androidx.compose.foundation.layout.Box
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.MenuDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.core.content.pm.PackageInfoCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.InkButton
import org.onebusaway.vehicletracker.ui.components.QuietButton

/** The build as the menu names it, "0.1.0 (1)", with "?" for a build that has no version name. */
internal fun versionText(name: String?, code: Long): String = "${name ?: "?"} ($code)"

/**
 * The account menu at the end of each picker step's header, the same as iOS's: who is signed in, to which server,
 * which build, and Sign out. There is none on Tracking or Resume, so a driver ends the trip first. [enabled] is false
 * while a start is in flight, which a sign-out would leave started for nobody.
 */
@Composable
fun AccountMenu(onSignedOut: () -> Unit, enabled: Boolean = true, viewModel: AccountViewModel = hiltViewModel()) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val version = remember(context) {
        // The overload taking flags needs API 33.
        @Suppress("DEPRECATION")
        val info = context.packageManager.getPackageInfo(context.packageName, 0)
        versionText(info.versionName, PackageInfoCompat.getLongVersionCode(info))
    }
    AccountMenuContent(state, version, enabled, onSignOut = viewModel::onSignOut, onSignedOut = onSignedOut)
}

/**
 * Sign out asks first, since the phone is usually about to go to the next driver. Once confirmed the dialog stays,
 * with a spinner, until the phone has forgotten the driver, and Back and taps outside it do nothing: nothing behind it
 * can start a trip meanwhile. [onSignedOut] is the phone having forgotten the driver.
 */
@Composable
fun AccountMenuContent(
    state: AccountUiState,
    version: String,
    enabled: Boolean,
    onSignOut: () -> Unit,
    onSignedOut: () -> Unit,
) {
    // Not a tap: it fires once the phone has forgotten the driver.
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }

    var expanded by remember { mutableStateOf(false) }
    var confirming by rememberSaveable { mutableStateOf(false) }

    Box {
        IconButton(onClick = { expanded = true }, enabled = enabled) {
            Icon(painterResource(R.drawable.ic_account_circle), contentDescription = stringResource(R.string.account_menu))
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            state.email?.let { AboutItem(it) }
            state.server?.let { AboutItem(it, R.drawable.ic_dns) }
            AboutItem(stringResource(R.string.account_version, version), R.drawable.ic_info)
            HorizontalDivider()
            DropdownMenuItem(
                text = { Text(stringResource(R.string.account_sign_out)) },
                leadingIcon = { Icon(painterResource(R.drawable.ic_logout), contentDescription = null) },
                onClick = {
                    expanded = false
                    confirming = true
                },
            )
        }
    }

    if (confirming) {
        AlertDialog(
            onDismissRequest = { if (!state.signingOut) confirming = false },
            title = { Text(stringResource(R.string.account_sign_out_title)) },
            text = { Text(stringResource(R.string.account_sign_out_message)) },
            confirmButton = {
                InkButton(stringResource(R.string.account_sign_out), onClick = onSignOut, busy = state.signingOut)
            },
            dismissButton = {
                QuietButton(
                    stringResource(R.string.account_sign_out_cancel),
                    onClick = { confirming = false },
                    enabled = !state.signingOut,
                )
            },
        )
    }
}

/** A line of the menu that tells rather than does: disabled, in a colour that still reads as text. */
@Composable
private fun AboutItem(text: String, @DrawableRes icon: Int? = null) {
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    DropdownMenuItem(
        text = { Text(text) },
        onClick = {},
        leadingIcon = icon?.let { { Icon(painterResource(it), contentDescription = null) } },
        enabled = false,
        colors = MenuDefaults.itemColors(disabledTextColor = muted, disabledLeadingIconColor = muted),
    )
}
