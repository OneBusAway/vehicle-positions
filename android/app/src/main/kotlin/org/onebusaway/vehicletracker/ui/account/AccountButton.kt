package org.onebusaway.vehicletracker.ui.account

import android.content.Context
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.core.content.pm.PackageInfoCompat
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.InkButton
import org.onebusaway.vehicletracker.ui.components.QuietButton

/** The build as Settings names it, "0.1.0 (1)", with "?" for a build that has no version name. */
internal fun versionText(name: String?, code: Long): String = "${name ?: "?"} ($code)"

/** This build's [versionText], as Settings and Share diagnostics show it. It asks the package manager. */
internal fun appVersion(context: Context): String {
    // The overload taking flags needs API 33.
    @Suppress("DEPRECATION")
    val info = context.packageManager.getPackageInfo(context.packageName, 0)
    return versionText(info.versionName, PackageInfoCompat.getLongVersionCode(info))
}

/**
 * Opens Settings. Provided on the steps of choosing a trip, so the button there opens it without each step passing it
 * on.
 */
internal val LocalOpenSettings = compositionLocalOf<() -> Unit> { {} }

/**
 * The account button at the end of each picker step's header, which opens Settings. There is none on Tracking or Resume,
 * so a driver ends the trip first, and Settings never opens while the vehicle moves. [enabled] is false while a start is
 * in flight, which a sign-out would leave started for nobody.
 */
@Composable
fun AccountButton(enabled: Boolean = true) {
    IconButton(onClick = LocalOpenSettings.current, enabled = enabled) {
        Icon(painterResource(R.drawable.ic_account_circle), contentDescription = stringResource(R.string.account_menu))
    }
}

/**
 * Sign out asks first, since the phone is usually about to go to the next driver. Once confirmed the dialog stays, with a
 * spinner, until the phone has forgotten the driver, and Back and taps outside it do nothing: nothing behind it can start
 * a trip meanwhile.
 */
@Composable
internal fun SignOutDialog(signingOut: Boolean, onSignOut: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = { if (!signingOut) onDismiss() },
        title = { Text(stringResource(R.string.account_sign_out_title)) },
        text = { Text(stringResource(R.string.account_sign_out_message)) },
        confirmButton = { InkButton(stringResource(R.string.account_sign_out), onClick = onSignOut, busy = signingOut) },
        dismissButton = { QuietButton(stringResource(R.string.account_sign_out_cancel), onClick = onDismiss, enabled = !signingOut) },
    )
}
