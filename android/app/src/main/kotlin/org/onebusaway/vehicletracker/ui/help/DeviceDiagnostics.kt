package org.onebusaway.vehicletracker.ui.help

import android.content.Context
import android.os.Build
import androidx.core.app.NotificationManagerCompat
import org.onebusaway.vehicletracker.ui.account.appVersion
import org.onebusaway.vehicletracker.ui.permissions.grantsOf
import org.onebusaway.vehicletracker.ui.permissions.locationServicesOn
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.util.Locale

private val MADE_AT = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm xxx", Locale.ROOT)

/** What [diagnosticsText] says about this build and this phone right now. Asks the package manager: not on the main thread. */
internal fun deviceFacts(context: Context): DeviceFacts = DeviceFacts(
    madeAt = MADE_AT.format(ZonedDateTime.now()),
    version = appVersion(context),
    androidRelease = Build.VERSION.RELEASE,
    sdk = Build.VERSION.SDK_INT,
    phone = "${Build.MANUFACTURER} ${Build.MODEL}",
    grants = grantsOf(context),
    notificationsOn = NotificationManagerCompat.from(context).areNotificationsEnabled(),
    locationServicesOn = locationServicesOn(context),
)
