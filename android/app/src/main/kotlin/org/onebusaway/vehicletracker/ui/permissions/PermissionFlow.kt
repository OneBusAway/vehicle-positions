package org.onebusaway.vehicletracker.ui.permissions

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.IntentSender
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.activity.result.IntentSenderRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.core.content.ContextCompat
import androidx.activity.compose.rememberLauncherForActivityResult
import com.google.android.gms.common.api.ResolvableApiException
import com.google.android.gms.location.LocationRequest
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.LocationSettingsRequest
import com.google.android.gms.location.Priority
import org.onebusaway.vehicletracker.R

private const val LOCATION_INTERVAL_MS = 10_000L

private enum class Stage {
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

/** Handle returned by [rememberPermissionFlow]; call [begin] to kick off the staged sequence. */
class PermissionFlowController internal constructor(private val start: () -> Unit) {
    fun begin() = start()
}

private fun hasAnyLocationPermission(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.ACCESS_FINE_LOCATION) ==
        PackageManager.PERMISSION_GRANTED ||
        ContextCompat.checkSelfPermission(context, Manifest.permission.ACCESS_COARSE_LOCATION) ==
        PackageManager.PERMISSION_GRANTED

private fun needsBackgroundLocationStage(context: Context): Boolean =
    Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q &&
        hasAnyLocationPermission(context) &&
        ContextCompat.checkSelfPermission(context, Manifest.permission.ACCESS_BACKGROUND_LOCATION) !=
        PackageManager.PERMISSION_GRANTED

/**
 * Drives the staged permission + location-services sequence required before a trip can start:
 * 1. Fine+coarse location together (only stage whose full decline is blocking).
 * 2. Background location, settings-directed, non-blocking decline.
 * 3. POST_NOTIFICATIONS on API 33+, non-blocking decline.
 * 4. Battery-optimization exemption, non-blocking decline.
 * 5. Device location-services-on check with a resolution dialog; must succeed to proceed.
 *
 * Renders any explanation/warning dialogs needed mid-flow as a side effect of composition.
 * [onReady] fires once every stage has been satisfied or non-blockingly declined.
 */
@Composable
fun rememberPermissionFlow(
    onReady: () -> Unit,
    onCancelled: () -> Unit = {},
): PermissionFlowController {
    val context = LocalContext.current
    var stage by remember { mutableStateOf(Stage.IDLE) }
    val latestOnReady by rememberUpdatedState(onReady)
    val latestOnCancelled by rememberUpdatedState(onCancelled)

    val locationRequest = remember {
        LocationRequest.Builder(Priority.PRIORITY_HIGH_ACCURACY, LOCATION_INTERVAL_MS).build()
    }
    val settingsClient = remember { LocationServices.getSettingsClient(context) }

    val locationPermsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { results ->
        val fine = results[Manifest.permission.ACCESS_FINE_LOCATION] == true
        val coarse = results[Manifest.permission.ACCESS_COARSE_LOCATION] == true
        stage = when {
            fine -> if (needsBackgroundLocationStage(context)) Stage.BACKGROUND_EXPLAIN else Stage.REQUEST_NOTIFICATIONS
            coarse -> Stage.APPROX_WARNING
            else -> Stage.LOCATION_DENIED
        }
    }

    val backgroundLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) {
        // Decline is non-blocking (degraded restart path) — always proceed.
        stage = Stage.REQUEST_NOTIFICATIONS
    }

    val notificationsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        stage = if (granted) nextAfterNotifications(context) else Stage.NOTIFICATIONS_DENIED_INFO
    }

    val batteryLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) {
        // Decline is non-blocking (warn only) — always proceed.
        stage = Stage.CHECK_LOCATION_SETTINGS
    }

    val settingsResolutionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartIntentSenderForResult(),
    ) { result ->
        stage = if (result.resultCode == Activity.RESULT_OK) Stage.READY else Stage.LOCATION_SETTINGS_OFF
    }

    LaunchedEffect(stage) {
        when (stage) {
            Stage.REQUEST_LOCATION -> locationPermsLauncher.launch(
                arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
            )
            Stage.REQUEST_BACKGROUND -> backgroundLauncher.launch(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
            Stage.REQUEST_NOTIFICATIONS -> {
                if (needsNotificationsRequest(context)) {
                    notificationsLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                    // launcher result drives the next transition
                } else {
                    stage = nextAfterNotifications(context)
                }
            }
            Stage.REQUEST_BATTERY -> batteryLauncher.launch(batteryOptimizationIntent(context))
            Stage.CHECK_LOCATION_SETTINGS -> {
                val request = LocationSettingsRequest.Builder().addLocationRequest(locationRequest).build()
                settingsClient.checkLocationSettings(request)
                    .addOnSuccessListener { stage = Stage.READY }
                    .addOnFailureListener { exception ->
                        if (exception is ResolvableApiException) {
                            try {
                                settingsResolutionLauncher.launch(IntentSenderRequest.Builder(exception.resolution).build())
                            } catch (e: IntentSender.SendIntentException) {
                                stage = Stage.LOCATION_SETTINGS_OFF
                            }
                        } else {
                            stage = Stage.LOCATION_SETTINGS_OFF
                        }
                    }
            }
            Stage.READY -> {
                latestOnReady()
                stage = Stage.IDLE
            }
            else -> Unit
        }
    }

    when (stage) {
        Stage.APPROX_WARNING -> PermissionDialog(
            title = stringResource(R.string.permission_location_approx_title),
            message = stringResource(R.string.permission_location_approx_message),
            confirmText = stringResource(R.string.permission_grant_precise_button),
            onConfirm = { stage = Stage.REQUEST_LOCATION },
            dismissText = stringResource(R.string.permission_continue_button),
            onDismiss = {
                stage = if (needsBackgroundLocationStage(context)) Stage.BACKGROUND_EXPLAIN else Stage.REQUEST_NOTIFICATIONS
            },
        )
        Stage.LOCATION_DENIED -> PermissionDialog(
            title = stringResource(R.string.permission_location_denied_title),
            message = stringResource(R.string.permission_location_denied_message),
            confirmText = stringResource(R.string.permission_retry_button),
            onConfirm = { stage = Stage.REQUEST_LOCATION },
            dismissText = stringResource(R.string.permission_cancel_button),
            onDismiss = {
                stage = Stage.IDLE
                latestOnCancelled()
            },
        )
        Stage.BACKGROUND_EXPLAIN -> PermissionDialog(
            title = stringResource(R.string.permission_background_title),
            message = stringResource(R.string.permission_background_message),
            confirmText = stringResource(R.string.permission_continue_button),
            onConfirm = { stage = Stage.REQUEST_BACKGROUND },
            dismissText = stringResource(R.string.permission_skip_button),
            onDismiss = { stage = Stage.REQUEST_NOTIFICATIONS },
        )
        Stage.NOTIFICATIONS_DENIED_INFO -> PermissionDialog(
            title = null,
            message = stringResource(R.string.permission_notifications_denied_message),
            confirmText = stringResource(R.string.permission_ok_button),
            onConfirm = { stage = nextAfterNotifications(context) },
            dismissText = null,
            onDismiss = { stage = nextAfterNotifications(context) },
        )
        Stage.BATTERY_EXPLAIN -> PermissionDialog(
            title = stringResource(R.string.permission_battery_title),
            message = stringResource(R.string.permission_battery_message),
            confirmText = stringResource(R.string.permission_continue_button),
            onConfirm = { stage = Stage.REQUEST_BATTERY },
            dismissText = stringResource(R.string.permission_skip_button),
            onDismiss = { stage = Stage.CHECK_LOCATION_SETTINGS },
        )
        Stage.LOCATION_SETTINGS_OFF -> PermissionDialog(
            title = stringResource(R.string.permission_location_services_off_title),
            message = stringResource(R.string.permission_location_services_off_message),
            confirmText = stringResource(R.string.permission_retry_button),
            onConfirm = { stage = Stage.CHECK_LOCATION_SETTINGS },
            dismissText = stringResource(R.string.permission_cancel_button),
            onDismiss = {
                stage = Stage.IDLE
                latestOnCancelled()
            },
        )
        else -> Unit
    }

    return remember { PermissionFlowController { stage = Stage.REQUEST_LOCATION } }
}

private fun needsNotificationsRequest(context: Context): Boolean =
    Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
        PackageManager.PERMISSION_GRANTED

private fun nextAfterNotifications(context: Context): Stage {
    val powerManager = context.getSystemService(PowerManager::class.java)
    val batteryExempt = powerManager?.isIgnoringBatteryOptimizations(context.packageName) == true
    return if (batteryExempt) Stage.CHECK_LOCATION_SETTINGS else Stage.BATTERY_EXPLAIN
}

private fun batteryOptimizationIntent(context: Context) =
    Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
        data = Uri.parse("package:${context.packageName}")
    }

@Composable
private fun PermissionDialog(
    title: String?,
    message: String,
    confirmText: String,
    onConfirm: () -> Unit,
    dismissText: String?,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = title?.let { { Text(it) } },
        text = { Text(message) },
        confirmButton = {
            TextButton(onClick = onConfirm) { Text(confirmText) }
        },
        dismissButton = dismissText?.let {
            { TextButton(onClick = onDismiss) { Text(it) } }
        },
    )
}
