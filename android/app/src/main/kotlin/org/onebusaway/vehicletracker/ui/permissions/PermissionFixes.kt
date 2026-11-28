package org.onebusaway.vehicletracker.ui.permissions

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.IntentSender
import android.os.Build
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.IntentSenderRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.ActivityCompat
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.createSavedStateHandle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.google.android.gms.common.api.ResolvableApiException
import com.google.android.gms.location.LocationServices
import kotlinx.coroutines.flow.StateFlow
import org.onebusaway.vehicletracker.data.AppLog

private const val TAG = "PermissionFixes"
private const val ONLY_IN_SETTINGS = "onlyInSettings"

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

/**
 * The rows only the phone's settings can change, as far as Android's answers have shown. The activity keeps them, not
 * a page, so a refusal for good on Settings or on Permission check is known on both, each time they open. They are saved
 * with the activity's state.
 */
internal class PermissionAnswers(private val saved: SavedStateHandle) : ViewModel() {
    val onlyInSettings: StateFlow<Set<PermissionRow>> = saved.getStateFlow(ONLY_IN_SETTINGS, emptySet())

    /** Android has answered an ask for [row], as [afterAsking] reads it. */
    fun answered(row: PermissionRow, allowed: Boolean, rationale: Boolean) {
        saved[ONLY_IN_SETTINGS] = onlyInSettings.value.afterAsking(row, allowed, rationale)
    }
}

/**
 * Fixes one row at a time, as the driver taps it: Permission check's, or the one Settings' header names. [noticeFor] is
 * the row whose ask waits on the location notice, while it is up.
 */
internal class PermissionFixes(
    val onlyInSettings: Set<PermissionRow>,
    val noticeFor: PermissionRow? = null,
    private val onAgree: () -> Unit = {},
    private val onDecline: () -> Unit = {},
    private val onFix: (PermissionRow) -> Unit,
) {
    fun fix(row: PermissionRow) = onFix(row)

    /** Agree on the notice: Android is asked for the row it waited for. */
    fun agreeToNotice() = onAgree()

    /** Not now, or Back, on the notice: nothing is asked. */
    fun declineNotice() = onDecline()
}

/** The rows Android asks about with a location prompt, which the location notice comes before. */
internal val PermissionRow.needsNotice: Boolean
    get() = this == PermissionRow.LOCATION || this == PermissionRow.PRECISE || this == PermissionRow.BACKGROUND

/**
 * Asks for nothing until a row is fixed, and then for that row alone. [onAnswer] fires once Android has answered. A
 * location row waits for the location [notice] until this phone has agreed to it.
 */
@Composable
internal fun rememberPermissionFixes(onAnswer: () -> Unit, notice: LocationNoticeState): PermissionFixes {
    val activity = LocalActivity.current
    return rememberPermissionFixes(
        onAnswer,
        notice,
        showsRationale = { row ->
            activity != null && permissionsOf(row).any { ActivityCompat.shouldShowRequestPermissionRationale(activity, it) }
        },
    )
}

/**
 * [showsRationale] is whether Android would show its prompt for a row again, and [readGrants] what the phone has
 * granted, which a test cannot set by answering a real prompt.
 */
@Composable
internal fun rememberPermissionFixes(
    onAnswer: () -> Unit,
    notice: LocationNoticeState,
    showsRationale: (PermissionRow) -> Boolean,
    readGrants: (Context) -> Grants = ::grantsOf,
): PermissionFixes {
    val context = LocalContext.current
    val latestOnAnswer by rememberUpdatedState(onAnswer)
    val settingsClient = remember { LocationServices.getSettingsClient(context) }
    val permissionAnswers = activityPermissionAnswers()
    val onlyInSettings by permissionAnswers.onlyInSettings.collectAsState()
    // Saved with them: the answer to a prompt that was out comes back to the activity that replaces this one.
    var asking by rememberSaveable { mutableStateOf<PermissionRow?>(null) }
    var noticeFor by rememberSaveable { mutableStateOf<PermissionRow?>(null) }
    val latestNotice by rememberUpdatedState(notice)

    val permissionsLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { answers ->
        val row = asking ?: return@rememberLauncherForActivityResult
        asking = null
        // Android leaves an interrupted ask without any answer.
        if (answers.isNotEmpty()) permissionAnswers.answered(row, readGrants(context).allows(row), showsRationale(row))
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

    fun ask(row: PermissionRow) {
        // One prompt at a time: Android answers a second ask with nothing.
        if (asking != null) return
        asking = row
        permissionsLauncher.launch(permissionsOf(row))
    }

    return PermissionFixes(
        onlyInSettings = onlyInSettings,
        noticeFor = noticeFor,
        onAgree = {
            val row = noticeFor
            noticeFor = null
            latestNotice.agree()
            row?.let(::ask)
        },
        onDecline = { noticeFor = null },
    ) { row ->
        when (fixOf(row, onlyInSettings)) {
            Fix.TURN_ON -> turnOnLocation()
            Fix.OPEN_SETTINGS -> context.open(settingsOf(row, context))
            Fix.ALLOW -> when {
                row == PermissionRow.BATTERY -> context.open(batteryOptimizationIntent(context))
                // Unread, it shows too: better asked twice than a prompt with no notice before it.
                row.needsNotice && latestNotice.agreed != true -> noticeFor = row
                else -> ask(row)
            }
        }
    }
}

/** The activity's [PermissionAnswers]: the same one on each page, and each time a page opens. */
@Composable
private fun activityPermissionAnswers(): PermissionAnswers {
    val activity = checkNotNull(LocalActivity.current as? ComponentActivity) { "Permission fixes need the app's activity" }
    return viewModel(activity) { PermissionAnswers(createSavedStateHandle()) }
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
