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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LifecycleResumeEffect
import com.google.android.gms.common.api.ResolvableApiException
import com.google.android.gms.location.LocationRequest
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.LocationSettingsRequest
import com.google.android.gms.location.Priority

private const val LOCATION_INTERVAL_MS = 10_000L

/**
 * The checklist before a trip, as [rememberPermissionFlow] runs it: whether it is on screen, where the sequence is, and
 * what the phone has granted. The board under it calls [start] when a run is tapped.
 */
@Stable
class PermissionFlowController internal constructor(private val readGrants: () -> Grants) {
    internal var flow by mutableStateOf(PermissionFlowState())
        private set
    internal var grants by mutableStateOf(readGrants())
        private set
    var visible by mutableStateOf(false)
        private set

    internal val checklist: List<ChecklistRow> get() = flow.checklist(grants)
    internal val canContinue: Boolean get() = flow.continueEvent != null

    internal fun dispatch(event: PermissionEvent) {
        grants = readGrants()
        flow = flow.next(event, grants)
        // A run that started without the checklist shows it once a step needs the driver.
        if (flow.waitsForDriver) visible = true
    }

    /** A run was tapped: with nothing missing the run starts straight away, as it did before; otherwise it explains first. */
    fun start() {
        grants = readGrants()
        flow = PermissionFlowState()
        visible = !grants.hasEverything
        if (!visible) dispatch(PermissionEvent.Begin)
    }

    internal fun onContinue() {
        flow.continueEvent?.let(::dispatch)
    }

    internal fun onRowAction() = dispatch(PermissionEvent.AskAgain)

    /** Back leaves the checklist and the run with it. */
    fun cancel() {
        visible = false
        dispatch(PermissionEvent.Cancel)
    }

    internal fun started() {
        visible = false
        dispatch(PermissionEvent.Started)
    }

    /** Something may have been changed in Android's settings while the app was away. */
    internal fun refresh() {
        grants = readGrants()
    }
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
 * Runs the sequence in [next] that must pass before a trip can start, launching Android's prompts as the steps reach
 * them while the checklist stays on screen beneath. [onReady] fires once every step has been granted, or declined where
 * declining is allowed.
 */
@Composable
fun rememberPermissionFlow(onReady: () -> Unit): PermissionFlowController {
    val context = LocalContext.current
    val activity = LocalActivity.current
    val controller = remember { PermissionFlowController { grantsOf(context) } }
    val latestOnReady by rememberUpdatedState(onReady)

    val locationRequest = remember {
        LocationRequest.Builder(Priority.PRIORITY_HIGH_ACCURACY, LOCATION_INTERVAL_MS).build()
    }
    val settingsClient = remember { LocationServices.getSettingsClient(context) }

    // Read as each ask goes out: with the one read after the answer, it tells a refusal for good from a dismissed prompt.
    var rationaleBeforeAsk by remember { mutableStateOf(false) }
    val locationPermsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { results ->
        controller.dispatch(
            PermissionEvent.LocationAnswered(
                fine = results[Manifest.permission.ACCESS_FINE_LOCATION] == true,
                coarse = results[Manifest.permission.ACCESS_COARSE_LOCATION] == true,
                rationaleBefore = rationaleBeforeAsk,
                rationaleAfter = activity.showsLocationRationale(),
            ),
        )
    }

    val appSettingsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { controller.dispatch(PermissionEvent.SettingsClosed) }

    val backgroundLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { controller.dispatch(PermissionEvent.BackgroundAnswered) }

    val notificationsLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { controller.dispatch(PermissionEvent.NotificationsAnswered) }

    val batteryLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { controller.dispatch(PermissionEvent.BatteryAnswered) }

    val settingsResolutionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartIntentSenderForResult(),
    ) { result -> controller.dispatch(PermissionEvent.LocationServicesChecked(on = result.resultCode == Activity.RESULT_OK)) }

    LifecycleResumeEffect(controller) {
        controller.refresh()
        onPauseOrDispose {}
    }

    LaunchedEffect(controller.flow.step) {
        when (controller.flow.step) {
            PermissionStep.REQUEST_LOCATION -> {
                rationaleBeforeAsk = activity.showsLocationRationale()
                locationPermsLauncher.launch(LOCATION_PERMISSIONS)
            }
            PermissionStep.OPEN_APP_SETTINGS -> appSettingsLauncher.launch(appDetailsIntent(context))
            PermissionStep.REQUEST_BACKGROUND -> backgroundLauncher.launch(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
            PermissionStep.REQUEST_NOTIFICATIONS -> notificationsLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            PermissionStep.REQUEST_BATTERY -> batteryLauncher.launch(batteryOptimizationIntent(context))
            PermissionStep.CHECK_LOCATION_SETTINGS -> {
                val request = LocationSettingsRequest.Builder().addLocationRequest(locationRequest).build()
                settingsClient.checkLocationSettings(request)
                    .addOnSuccessListener { controller.dispatch(PermissionEvent.LocationServicesChecked(on = true)) }
                    .addOnFailureListener { exception ->
                        if (exception is ResolvableApiException) {
                            try {
                                settingsResolutionLauncher.launch(IntentSenderRequest.Builder(exception.resolution).build())
                            } catch (e: IntentSender.SendIntentException) {
                                controller.dispatch(PermissionEvent.LocationServicesChecked(on = false))
                            }
                        } else {
                            controller.dispatch(PermissionEvent.LocationServicesChecked(on = false))
                        }
                    }
            }
            PermissionStep.READY -> {
                latestOnReady()
                controller.started()
            }
            else -> Unit
        }
    }

    return controller
}

// Fine and coarse are asked together, as Android 12 and later require.
private val LOCATION_PERMISSIONS = arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION)

private fun Activity?.showsLocationRationale(): Boolean =
    this != null && LOCATION_PERMISSIONS.any { ActivityCompat.shouldShowRequestPermissionRationale(this, it) }

private fun appDetailsIntent(context: Context) =
    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null))

private fun batteryOptimizationIntent(context: Context) =
    Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
        data = Uri.parse("package:${context.packageName}")
    }
