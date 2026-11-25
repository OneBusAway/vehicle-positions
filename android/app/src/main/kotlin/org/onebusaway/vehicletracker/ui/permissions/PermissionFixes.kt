package org.onebusaway.vehicletracker.ui.permissions

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.IntentSender
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.IntentSenderRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.ActivityCompat
import com.google.android.gms.common.api.ResolvableApiException
import com.google.android.gms.location.LocationServices
import org.onebusaway.vehicletracker.data.AppLog

private const val TAG = "PermissionFixes"

/** What fixes a row the phone does not allow: Android asks, its settings open, or location turns on for the whole phone. */
internal enum class Fix { ALLOW, OPEN_SETTINGS, TURN_ON }

internal fun fixOf(row: PermissionRow, onlyInSettings: Set<PermissionRow>): Fix = when (row) {
    PermissionRow.LOCATION_SERVICES -> Fix.TURN_ON
    in onlyInSettings -> Fix.OPEN_SETTINGS
    else -> Fix.ALLOW
}

/**
 * The rows only the phone's settings can change, once Android has answered an ask for [row]. With a [rationale] it would
 * show its prompt again. With none it would not, or the driver left the prompt without an answer, and then the settings
 * can change it as well.
 */
internal fun Set<PermissionRow>.afterAsking(row: PermissionRow, allowed: Boolean, rationale: Boolean): Set<PermissionRow> =
    if (allowed || rationale) this - row else this + row

/** Fixes one row at a time, as the driver taps it: Permission check's, or the one Settings' header names. */
internal class PermissionFixes(val onlyInSettings: Set<PermissionRow>, private val onFix: (PermissionRow) -> Unit) {
    fun fix(row: PermissionRow) = onFix(row)
}

/** Asks for nothing until a row is fixed, and then for that row alone. [onAnswer] fires once Android has answered. */
@Composable
internal fun rememberPermissionFixes(onAnswer: () -> Unit): PermissionFixes {
    val context = LocalContext.current
    val activity = LocalActivity.current
    val latestOnAnswer by rememberUpdatedState(onAnswer)
    val settingsClient = remember { LocationServices.getSettingsClient(context) }
    var onlyInSettings by rememberSaveable { mutableStateOf(emptySet<PermissionRow>()) }
    // Saved with them: the answer to a prompt that was out comes back to the activity that replaces this one.
    var asking by rememberSaveable { mutableStateOf<PermissionRow?>(null) }

    val permissionsLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { answers ->
        val row = asking ?: return@rememberLauncherForActivityResult
        asking = null
        // Android leaves an interrupted ask without any answer.
        if (answers.isNotEmpty()) {
            val rationale = activity != null && permissionsOf(row).any { ActivityCompat.shouldShowRequestPermissionRationale(activity, it) }
            onlyInSettings = onlyInSettings.afterAsking(row, grantsOf(context).allows(row), rationale)
        }
        latestOnAnswer()
    }
    val locationServicesLauncher = rememberLauncherForActivityResult(ActivityResultContracts.StartIntentSenderForResult()) { latestOnAnswer() }

    fun turnOnLocation() {
        settingsClient.checkLocationSettings(locationSettingsRequest())
            .addOnSuccessListener { latestOnAnswer() }
            .addOnFailureListener { exception ->
                try {
                    val resolution = (exception as? ResolvableApiException)?.resolution
                    if (resolution == null) {
                        context.open(Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS))
                    } else {
                        locationServicesLauncher.launch(IntentSenderRequest.Builder(resolution).build())
                    }
                } catch (e: IntentSender.SendIntentException) {
                    context.open(Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS))
                }
            }
    }

    return PermissionFixes(onlyInSettings) { row ->
        when (fixOf(row, onlyInSettings)) {
            Fix.TURN_ON -> turnOnLocation()
            Fix.OPEN_SETTINGS -> context.open(settingsOf(row, context))
            Fix.ALLOW -> when {
                row == PermissionRow.BATTERY -> context.open(batteryOptimizationIntent(context))
                // One prompt at a time: Android answers a second ask with nothing.
                asking == null -> {
                    asking = row
                    permissionsLauncher.launch(permissionsOf(row))
                }
            }
        }
    }
}

/** What Android is asked for [row]. Only a row with permissions of its own is asked for this way. */
private fun permissionsOf(row: PermissionRow): Array<String> = when {
    row == PermissionRow.LOCATION || row == PermissionRow.PRECISE -> LOCATION_PERMISSIONS
    row == PermissionRow.BACKGROUND && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q -> arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
    row == PermissionRow.NOTIFICATIONS && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU -> arrayOf(Manifest.permission.POST_NOTIFICATIONS)
    else -> emptyArray()
}

private fun settingsOf(row: PermissionRow, context: Context): Intent =
    if (row == PermissionRow.NOTIFICATIONS) {
        Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)
    } else {
        appDetailsIntent(context)
    }

private fun Context.open(settings: Intent) {
    try {
        startActivity(settings)
    } catch (e: ActivityNotFoundException) {
        // A phone whose maker took the screen out. The driver can still find it in Settings.
        AppLog.w(TAG, "No settings screen to open for ${settings.action}", e)
    }
}
