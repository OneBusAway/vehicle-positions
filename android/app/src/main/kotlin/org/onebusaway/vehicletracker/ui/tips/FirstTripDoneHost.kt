package org.onebusaway.vehicletracker.ui.tips

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

/**
 * Puts "First trip done" up once the driver is [onASetupStep] after their first End trip, if that trip sent reports and
 * no tip is out. It is seen as soon as it is up.
 */
@Composable
fun FirstTripDoneHost(onASetupStep: Boolean, viewModel: FirstTripDoneViewModel = hiltViewModel()) {
    val trip by viewModel.trip.collectAsStateWithLifecycle()
    var up by remember { mutableStateOf<FinishedTrip?>(null) }
    LaunchedEffect(trip, onASetupStep) {
        val finished = trip
        if (up == null && finished != null && onASetupStep && viewModel.onShown(finished)) up = finished
    }
    up?.let { shown ->
        FirstTripDoneSheet(
            sent = shown.sent,
            onClose = {
                up = null
                viewModel.onClosed()
            },
        )
    }
}
