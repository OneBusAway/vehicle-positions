package org.onebusaway.vehicletracker.service

import android.app.Notification
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.os.IBinder
import android.util.Log
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
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.loadFor
import org.onebusaway.vehicletracker.engine.AdherenceTracker
import org.onebusaway.vehicletracker.ui.routes.routeColorArgb
import javax.inject.Inject

private const val TAG = "LocationTrackingService"
private const val LIVE_FIX_INTERVAL_MS = 1_000L

/**
 * Claims foreground status with [startForeground], or asks the driver to open the app when Android refuses it, and
 * says whether it was claimed.
 *
 * Taking location "all the time" away during a trip kills the app, and Android starts the START_STICKY service again
 * in the background with location only while in use. From Android 14, startForeground throws when Android does not
 * let that restart use location, as after [RestartTracking] started the service. Android is asked rather than the
 * permissions checked first: it lets the same restart keep location for a trip started in the app, and the
 * permissions are the same in both cases.
 */
internal fun startForegroundOrAskDriver(startForeground: () -> Unit, resumeNotifier: ResumeNotifier): Boolean {
    // Only the two ways Android documents refusing the start, as in RestartTracking: anything else is a bug.
    try {
        startForeground()
    } catch (e: IllegalStateException) {
        // ForegroundServiceStartNotAllowedException, from Android 12, is one.
        Log.w(TAG, "Android refused the tracking service foreground status", e)
        resumeNotifier.askToResume()
        return false
    } catch (e: SecurityException) {
        Log.w(TAG, "Android refused the tracking service a permission it needs", e)
        resumeNotifier.askToResume()
        return false
    }
    return true
}

/**
 * Foreground service that owns the fused-location request and hands each fix to [TripReporter]
 * and [AdherenceTracker]. Framework-glue shell only: the send loop / status machine lives in
 * [TripReporter] (Task 6), the per-fix judgement in [AdherenceTracker] and the answer to a refused
 * foreground start in [startForegroundOrAskDriver], all covered by JVM tests; nothing else here
 * is separately unit tested.
 *
 * It asks for a fix every second while the tracking screen is open, so the map and the panel
 * move with the vehicle, and every [REPORT_INTERVAL_MS] otherwise.
 */
@AndroidEntryPoint
class LocationTrackingService : Service() {

    @Inject lateinit var tripReporter: TripReporter
    @Inject lateinit var tripStateStore: TripStateStore
    @Inject lateinit var tripGeometryStore: TripGeometryStore
    @Inject lateinit var trackingRepository: TrackingRepository

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private lateinit var fusedClient: FusedLocationProviderClient
    private lateinit var notification: TrackingNotification
    private lateinit var connectivityManager: ConnectivityManager

    private var activeTrip: ActiveTrip? = null
    private var adherenceTracker: AdherenceTracker? = null
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
            // Judged here, beside the report rather than inside it: the reporter is about reaching
            // the server, and adherence holds with no network at all.
            adherenceTracker?.onFix(fix)?.let { adherence ->
                trackingRepository.update { it.copy(adherence = adherence) }
            }
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
        val foreground = startForegroundOrAskDriver(
            startForeground = {
                ServiceCompat.startForeground(
                    this,
                    NOTIFICATION_ID,
                    activeNotification(),
                    ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION,
                )
            },
            resumeNotifier = ::askToResume,
        )
        if (!foreground) {
            // Nothing can report without foreground status, so the service stops for good. The
            // trip stays stored, so the app opens on Resume.
            stopSelf()
            return START_NOT_STICKY
        }
        notification.cancelResume()

        if (!initialized) {
            val trip = runBlocking { tripStateStore.activeTrip.first() }
            if (trip == null) {
                stopSelf()
                return START_NOT_STICKY
            }
            activeTrip = trip
            // Read off the phone rather than refetched, so adherence works offline and picks up
            // again after a relaunch, as on iOS.
            val geometry = runBlocking { tripGeometryStore.loadFor(trip) }
            adherenceTracker = AdherenceTracker(geometry)
            initialized = true
            trackingRepository.update {
                it.copy(active = true, tripStartedAtEpochSec = trip.startedAtEpochSec, geometry = geometry)
            }
            observeTrackingState()
        }

        if (activeTrip != null && !locationUpdatesActive) {
            startLocationUpdates()
        }

        return START_STICKY
    }

    private fun startLocationUpdates() {
        val intervalMs = if (trackingRepository.liveView.value) LIVE_FIX_INTERVAL_MS else REPORT_INTERVAL_MS
        // Asking again with the same callback replaces the request in place.
        val request = LocationRequest.Builder(Priority.PRIORITY_HIGH_ACCURACY, intervalMs).build()
        try {
            fusedClient.requestLocationUpdates(request, locationCallback, mainLooper)
            locationUpdatesActive = true
            postNotification()
        } catch (e: SecurityException) {
            // Background restart without ACCESS_BACKGROUND_LOCATION granted — the spec's degraded
            // path. Drop out of foreground state but keep the service alive; MainActivity calls
            // ServiceController.startTracking() again while it is in the foreground.
            Log.w(TAG, "Missing location permission for background restart; entering degraded mode", e)
            locationUpdatesActive = false
            askToResume()
        }
    }

    // The ongoing notification goes with the foreground status, and "Reports paused", under an id of
    // its own, takes its place. After a refused restart Android still holds the killed service's
    // notification on it, so this also takes that down rather than leave "Reporting" showing.
    private fun askToResume() {
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        notification.postResumeIfPermitted()
    }

    private fun observeTrackingState() {
        // Posted when what it says changes, at most once a fix: not for each report sent.
        trackingRepository.state
            .map { notificationText(it, activeTrip) }
            .distinctUntilChanged()
            .onEach { postNotification() }
            .launchIn(scope)
        // On the main thread, like every other call to startLocationUpdates.
        scope.launch(Dispatchers.Main) {
            trackingRepository.liveView.collect { if (locationUpdatesActive) startLocationUpdates() }
        }
    }

    private fun postNotification() {
        if (!locationUpdatesActive) return
        notification.postActiveIfPermitted(activeNotification())
    }

    private fun activeNotification(): Notification {
        val state = trackingRepository.state.value
        val routeColor = state.geometry?.routeColor?.let(::routeColorArgb)?.toInt()
        return notification.buildActiveNotification(notificationText(state, activeTrip), routeColor)
    }

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
