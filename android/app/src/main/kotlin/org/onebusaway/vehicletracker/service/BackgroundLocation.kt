package org.onebusaway.vehicletracker.service

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.content.ContextCompat

/**
 * Whether reports can start with no screen in front, which takes location "all the time". Before Android 10 location
 * is not split into while in use and all the time: having it is having it all.
 */
internal fun canReportInBackground(sdk: Int, fine: Boolean, coarse: Boolean, background: Boolean): Boolean =
    if (sdk < Build.VERSION_CODES.Q) fine || coarse else background

/** [canReportInBackground] with what this phone has granted right now. */
internal fun Context.canReportInBackground(): Boolean = canReportInBackground(
    sdk = Build.VERSION.SDK_INT,
    fine = granted(Manifest.permission.ACCESS_FINE_LOCATION),
    coarse = granted(Manifest.permission.ACCESS_COARSE_LOCATION),
    background = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q && granted(Manifest.permission.ACCESS_BACKGROUND_LOCATION),
)

private fun Context.granted(permission: String): Boolean =
    ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED
