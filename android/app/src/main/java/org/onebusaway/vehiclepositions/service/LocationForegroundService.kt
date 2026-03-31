package org.onebusaway.vehiclepositions.service

import android.app.Service
import android.content.Intent
import android.os.IBinder

// Full implementation added in Screen 1 PR
class LocationForegroundService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null
}