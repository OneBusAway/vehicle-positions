package org.onebusaway.vehicletracker.service

import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import com.google.android.gms.location.FusedLocationProviderClient
import com.google.android.gms.location.LocationAvailability
import com.google.android.gms.location.LocationCallback
import com.google.android.gms.location.LocationRequest
import com.google.android.gms.location.LocationResult
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.TripStateStore
import javax.inject.Inject

private const val TAG = "LocationTrackingService"
private const val LOCATION_INTERVAL_MS = 10_000L

/**
 * Foreground service that owns the fused-location request and hands each fix to [TripReporter].
 * Framework-glue shell only — the send loop / status machine lives in [TripReporter] (Task 6),
 * already covered by JVM tests; nothing here is separately unit tested.
 */
@AndroidEntryPoint
class LocationTrackingService : Service() {

    @Inject lateinit var tripReporter: TripReporter
    @Inject lateinit var tripStateStore: TripStateStore
    @Inject lateinit var trackingRepository: TrackingRepository

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private lateinit var fusedClient: FusedLocationProviderClient
    private lateinit var notification: TrackingNotification
    private lateinit var connectivityManager: ConnectivityManager

    private var activeTrip: ActiveTrip? = null
    private var initialized = false
    private var locationUpdatesActive = false

    private val locationCallback = object : LocationCallback() {
        override fun onLocationResult(result: LocationResult) {
            val loc = result.lastLocation ?: return
            val trip = activeTrip ?: return
            val fix = LocationFix(
                latitude = loc.latitude,
                longitude = loc.longitude,
                bearing = if (loc.hasBearing()) loc.bearing.toDouble() else null,
                speed = if (loc.hasSpeed()) loc.speed.toDouble() else null,
                accuracy = if (loc.hasAccuracy()) loc.accuracy.toDouble() else null,
                timeEpochSec = loc.time / 1000,
            )
            scope.launch { tripReporter.report(trip, fix) }
        }

        override fun onLocationAvailability(availability: LocationAvailability) {
            tripReporter.gpsAvailable(availability.isLocationAvailable)
        }
    }

    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onLost(network: Network) {
            // TripReporter owns send-result status; this is only the faster "we definitely have
            // no network" signal, so don't stomp a more specific problem already in effect.
            trackingRepository.update {
                if (it.problem == TrackingProblem.NONE) it.copy(problem = TrackingProblem.NO_NETWORK) else it
            }
        }
    }

    override fun onCreate() {
        super.onCreate()
        fusedClient = LocationServices.getFusedLocationProviderClient(this)
        notification = TrackingNotification(this)
        connectivityManager = getSystemService(ConnectivityManager::class.java)
        connectivityManager.registerDefaultNetworkCallback(networkCallback)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopTrackingWork()
            stopSelf()
            return START_NOT_STICKY
        }

        // Always claim foreground status first, per the location-type FGS contract, before doing
        // anything else — this also restores the "active" notification on a resume-from-degraded.
        ServiceCompat.startForeground(
            this,
            NOTIFICATION_ID,
            notification.buildActiveNotification(statusText(trackingRepository.state.value.problem)),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION,
        )

        if (!initialized) {
            val trip = runBlocking { tripStateStore.activeTrip.first() }
            if (trip == null) {
                stopSelf()
                return START_STICKY
            }
            activeTrip = trip
            initialized = true
            trackingRepository.update { it.copy(active = true, tripStartedAtEpochSec = trip.startedAtEpochSec) }
            observeTrackingState()
        }

        if (activeTrip != null && !locationUpdatesActive) {
            startLocationUpdates()
        }

        return START_STICKY
    }

    private fun startLocationUpdates() {
        val request = LocationRequest.Builder(Priority.PRIORITY_HIGH_ACCURACY, LOCATION_INTERVAL_MS).build()
        try {
            fusedClient.requestLocationUpdates(request, locationCallback, mainLooper)
            locationUpdatesActive = true
            postNotification()
        } catch (e: SecurityException) {
            // Background restart without ACCESS_BACKGROUND_LOCATION granted — the spec's degraded
            // path. Drop out of foreground state but keep the service (and its notification) alive;
            // MainActivity.onResume calls ServiceController.startTracking() again on next foreground.
            Log.w(TAG, "Missing location permission for background restart; entering degraded mode", e)
            locationUpdatesActive = false
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_DETACH)
            NotificationManagerCompat.from(this).notify(NOTIFICATION_ID, notification.buildResumeNotification())
        }
    }

    private fun observeTrackingState() {
        trackingRepository.state
            .map { it.problem }
            .distinctUntilChanged()
            .onEach { postNotification() }
            .launchIn(scope)
    }

    private fun postNotification() {
        if (!locationUpdatesActive) return
        val text = statusText(trackingRepository.state.value.problem)
        NotificationManagerCompat.from(this).notify(NOTIFICATION_ID, notification.buildActiveNotification(text))
    }

    private fun statusText(problem: TrackingProblem): String = getString(
        when (problem) {
            TrackingProblem.NONE -> R.string.tracking_status_connected
            TrackingProblem.NO_NETWORK -> R.string.tracking_status_no_network
            TrackingProblem.NO_GPS -> R.string.tracking_status_no_gps
            TrackingProblem.AUTH_EXPIRED -> R.string.tracking_status_auth_expired
            TrackingProblem.CLOCK_SKEW -> R.string.tracking_status_clock_skew
        },
    )

    private fun stopTrackingWork() {
        if (locationUpdatesActive) {
            fusedClient.removeLocationUpdates(locationCallback)
            locationUpdatesActive = false
        }
        trackingRepository.update { TrackingState() }
    }

    override fun onDestroy() {
        stopTrackingWork()
        runCatching { connectivityManager.unregisterNetworkCallback(networkCallback) }
        scope.cancel()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    companion object {
        const val ACTION_STOP = "org.onebusaway.vehicletracker.action.STOP_TRACKING"
    }
}
