package org.onebusaway.vehicletracker

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.map.MapRepository
import org.onebusaway.vehicletracker.data.map.refreshWhenSignedIn
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.service.rearmWhenActive
import org.onebusaway.vehicletracker.ui.AppNav
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    @Inject lateinit var serviceController: ServiceController
    @Inject lateinit var trackingRepository: TrackingRepository
    @Inject lateinit var sessionStore: SessionStore
    @Inject lateinit var mapRepository: MapRepository

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            AppTheme {
                // enableEdgeToEdge() draws content behind the system bars; without this, top/bottom
                // content (e.g. the tracking screen's status banner and End Trip button) can be
                // obscured by the status bar or gesture/navigation bar.
                Surface(modifier = Modifier.fillMaxSize().safeDrawingPadding()) {
                    AppNav()
                }
            }
        }
        // Re-arms a service that is running without location updates: the degraded restart
        // path (SecurityException in the service). Watched while resumed rather than checked once,
        // because a START_STICKY restart can turn active after the activity has resumed. A stored
        // trip with no service running is not started here: that shift was interrupted, and the
        // resume prompt asks the driver.
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.RESUMED) {
                launch { rearmWhenActive(trackingRepository.state, serviceController::startTracking) }
                // Fetches the agency's street map, or checks it is current, whenever the app comes
                // to the front signed in, when the driver signs in, and each day it stays in front.
                refreshWhenSignedIn(sessionStore.session, mapRepository::refresh)
            }
        }
    }
}
