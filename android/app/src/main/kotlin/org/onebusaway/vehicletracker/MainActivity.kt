package org.onebusaway.vehicletracker

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import dagger.hilt.android.AndroidEntryPoint
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.AppNav
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    @Inject lateinit var serviceController: ServiceController
    @Inject lateinit var trackingRepository: TrackingRepository

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
    }

    override fun onResume() {
        super.onResume()
        // Re-arms a service that is running without location updates: the degraded restart
        // path (SecurityException in the service). A stored trip with no service running is not
        // started here — that shift was interrupted, and the resume prompt asks the driver.
        if (trackingRepository.state.value.active) {
            serviceController.startTracking()
        }
    }
}
