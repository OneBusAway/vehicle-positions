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
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
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
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import com.google.android.gms.common.api.ResolvableApiException
import com.google.android.gms.location.LocationRequest
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.LocationSettingsRequest
import com.google.android.gms.location.Priority
import org.onebusaway.vehicletracker.R

private const val LOCATION_INTERVAL_MS = 10_000L

/** Handle returned by [rememberPermissionFlow]; call [begin] to kick off the staged sequence. */
class PermissionFlowController internal constructor(private val start: () -> Unit) {
    fun begin() = start()
}

private fun Context.granted(permission: String): Boolean =
    ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED

/** What the phone has granted right now. A permission Android does not have yet reads as not granted. */
private fun grantsOf(context: Context): Grants = Grants(
    sdk = Build.VERSION.SDK_INT,
    fine = context.granted(Manifest.permission.ACCESS_FINE_LOCATION),
    coarse = context.granted(Manifest.permission.ACCESS_COARSE_LOCATION),
    background = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q && context.granted(Manifest.permission.ACCESS_BACKGROUND_LOCATION),
    notifications = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && context.granted(Manifest.permission.POST_NOTIFICATIONS),
    batteryExempt = context.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(context.packageName) == true,
)

/**
 * Drives the sequence in [next] that must run before a trip can start, launching Android's prompts as the steps
 * reach them and showing the explanation and warning dialogs in between. [onReady] fires once every step has been
 * granted or declined where declining is allowed; [onCancelled] once the driver gives up at a step that is not.
 */
@Composable
fun rememberPermissionFlow(
    onReady: () -> Unit,
    onCancelled: () -> Unit = {},
): PermissionFlowController {
    val context = LocalContext.current
    val activity = LocalActivity.current
    var state by remember { mutableStateOf(PermissionFlowState()) }
    val latestOnReady by rememberUpdatedState(onReady)
    val latestOnCancelled by rememberUpdatedState(onCancelled)
    fun dispatch(event: PermissionEvent) {
        state = state.next(event, grantsOf(context))
    }

    val locationRequest = remember {
        LocationRequest.Builder(Priority.PRIORITY_HIGH_ACCURACY, LOCATION_INTERVAL_MS).build()
    }
    val settingsClient = remember { LocationServices.getSettingsClient(context) }

    val locationPermsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { results ->
        // Refused with no rationale to show is refused for good: Android will not ask again.
        val canAskAgain = activity != null && LOCATION_PERMISSIONS.any { ActivityCompat.shouldShowRequestPermissionRationale(activity, it) }
        dispatch(
            PermissionEvent.LocationAnswered(
                fine = results[Manifest.permission.ACCESS_FINE_LOCATION] == true,
                coarse = results[Manifest.permission.ACCESS_COARSE_LOCATION] == true,
                canAskAgain = canAskAgain,
            ),
        )
    }

    val backgroundLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { dispatch(PermissionEvent.BackgroundAnswered) }

    val notificationsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted -> dispatch(PermissionEvent.NotificationsAnswered(granted)) }

    val batteryLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { dispatch(PermissionEvent.BatteryAnswered) }

    val settingsResolutionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartIntentSenderForResult(),
    ) { result -> dispatch(PermissionEvent.LocationServicesChecked(on = result.resultCode == Activity.RESULT_OK)) }

    LaunchedEffect(state.step) {
        when (state.step) {
            PermissionStep.REQUEST_LOCATION -> locationPermsLauncher.launch(LOCATION_PERMISSIONS)
            PermissionStep.REQUEST_BACKGROUND -> backgroundLauncher.launch(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
            PermissionStep.REQUEST_NOTIFICATIONS -> notificationsLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            PermissionStep.REQUEST_BATTERY -> batteryLauncher.launch(batteryOptimizationIntent(context))
            PermissionStep.CHECK_LOCATION_SETTINGS -> {
                val request = LocationSettingsRequest.Builder().addLocationRequest(locationRequest).build()
                settingsClient.checkLocationSettings(request)
                    .addOnSuccessListener { dispatch(PermissionEvent.LocationServicesChecked(on = true)) }
                    .addOnFailureListener { exception ->
                        if (exception is ResolvableApiException) {
                            try {
                                settingsResolutionLauncher.launch(IntentSenderRequest.Builder(exception.resolution).build())
                            } catch (e: IntentSender.SendIntentException) {
                                dispatch(PermissionEvent.LocationServicesChecked(on = false))
                            }
                        } else {
                            dispatch(PermissionEvent.LocationServicesChecked(on = false))
                        }
                    }
            }
            PermissionStep.READY -> {
                latestOnReady()
                dispatch(PermissionEvent.Started)
            }
            else -> Unit
        }
    }

    when (state.step) {
        PermissionStep.APPROX_WARNING -> PermissionDialog(
            title = stringResource(R.string.permission_location_approx_title),
            message = stringResource(R.string.permission_location_approx_message),
            confirmText = stringResource(R.string.permission_grant_precise_button),
            onConfirm = { dispatch(PermissionEvent.AskAgain) },
            dismissText = stringResource(R.string.permission_continue_button),
            onDismiss = { dispatch(PermissionEvent.ContinueApproximate) },
        )
        PermissionStep.LOCATION_DENIED -> PermissionDialog(
            title = stringResource(R.string.permission_location_denied_title),
            message = stringResource(R.string.permission_location_denied_message),
            confirmText = stringResource(R.string.permission_retry_button),
            onConfirm = { dispatch(PermissionEvent.AskAgain) },
            dismissText = stringResource(R.string.permission_cancel_button),
            onDismiss = {
                dispatch(PermissionEvent.Cancel)
                latestOnCancelled()
            },
        )
        PermissionStep.BACKGROUND_EXPLAIN -> PermissionDialog(
            title = stringResource(R.string.permission_background_title),
            message = stringResource(R.string.permission_background_message),
            confirmText = stringResource(R.string.permission_continue_button),
            onConfirm = { dispatch(PermissionEvent.Explained) },
            dismissText = stringResource(R.string.permission_skip_button),
            onDismiss = { dispatch(PermissionEvent.NotNow) },
        )
        PermissionStep.NOTIFICATIONS_DENIED_INFO -> PermissionDialog(
            title = null,
            message = stringResource(R.string.permission_notifications_denied_message),
            confirmText = stringResource(R.string.permission_ok_button),
            onConfirm = { dispatch(PermissionEvent.Acknowledged) },
            dismissText = null,
            onDismiss = { dispatch(PermissionEvent.Acknowledged) },
        )
        PermissionStep.BATTERY_EXPLAIN -> PermissionDialog(
            title = stringResource(R.string.permission_battery_title),
            message = stringResource(R.string.permission_battery_message),
            confirmText = stringResource(R.string.permission_continue_button),
            onConfirm = { dispatch(PermissionEvent.Explained) },
            dismissText = stringResource(R.string.permission_skip_button),
            onDismiss = { dispatch(PermissionEvent.NotNow) },
        )
        PermissionStep.LOCATION_SETTINGS_OFF -> PermissionDialog(
            title = stringResource(R.string.permission_location_services_off_title),
            message = stringResource(R.string.permission_location_services_off_message),
            confirmText = stringResource(R.string.permission_retry_button),
            onConfirm = { dispatch(PermissionEvent.AskAgain) },
            dismissText = stringResource(R.string.permission_cancel_button),
            onDismiss = {
                dispatch(PermissionEvent.Cancel)
                latestOnCancelled()
            },
        )
        else -> Unit
    }

    return remember { PermissionFlowController { dispatch(PermissionEvent.Begin) } }
}

// Fine and coarse are asked together, as Android 12 and later require.
private val LOCATION_PERMISSIONS = arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION)

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
