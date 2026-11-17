package org.onebusaway.vehicletracker.ui.help

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.location.LocationManager
import android.os.Build
import android.os.PowerManager
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.core.location.LocationManagerCompat
import org.onebusaway.vehicletracker.ui.account.appVersion
import org.onebusaway.vehicletracker.ui.permissions.Grants
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter

private val MADE_AT = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm xxx")

/** What [diagnosticsText] says about this build and this phone right now. Asks the package manager: not on the main thread. */
internal fun deviceFacts(context: Context): DeviceFacts = DeviceFacts(
    madeAt = MADE_AT.format(ZonedDateTime.now()),
    version = appVersion(context),
    androidRelease = Build.VERSION.RELEASE,
    sdk = Build.VERSION.SDK_INT,
    phone = "${Build.MANUFACTURER} ${Build.MODEL}",
    grants = grantsOf(context),
    notificationsOn = NotificationManagerCompat.from(context).areNotificationsEnabled(),
    locationServicesOn = context.getSystemService(LocationManager::class.java)?.let(LocationManagerCompat::isLocationEnabled) == true,
)

// The checklist's reading of the same grants, which it keeps to itself.
private fun grantsOf(context: Context): Grants = Grants(
    sdk = Build.VERSION.SDK_INT,
    fine = context.granted(Manifest.permission.ACCESS_FINE_LOCATION),
    coarse = context.granted(Manifest.permission.ACCESS_COARSE_LOCATION),
    background = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q && context.granted(Manifest.permission.ACCESS_BACKGROUND_LOCATION),
    notifications = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && context.granted(Manifest.permission.POST_NOTIFICATIONS),
    batteryExempt = context.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(context.packageName) == true,
)

private fun Context.granted(permission: String): Boolean =
    ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED
