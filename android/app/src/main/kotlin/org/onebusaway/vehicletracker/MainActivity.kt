package org.onebusaway.vehicletracker

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.AppNav
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    @Inject lateinit var serviceController: ServiceController
    @Inject lateinit var tripStateStore: TripStateStore

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { AppTheme { AppNav() } }
    }

    override fun onResume() {
        super.onResume()
        // Covers both the initial cold-start-into-Tracking case and the degraded restart path
        // (SecurityException in the service): re-request tracking whenever a trip is active.
        lifecycleScope.launch {
            if (tripStateStore.activeTrip.first() != null) {
                serviceController.startTracking()
            }
        }
    }
}
