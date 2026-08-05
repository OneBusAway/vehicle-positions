package org.onebusaway.vehicletracker.ui.trip

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFlow

@Composable
fun TripSetupScreen(
    vehicleId: String,
    onTripStarted: () -> Unit,
    viewModel: TripSetupViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val permissionFlow = rememberPermissionFlow(
        onReady = { viewModel.onStartTrip(vehicleId, onTripStarted) },
    )
    TripSetupScreenContent(
        state = state,
        onRouteIdChange = viewModel::onRouteIdChange,
        onGtfsTripIdChange = viewModel::onGtfsTripIdChange,
        onRecentRouteClick = viewModel::onRouteIdChange,
        onStartTripClick = {
            if (state.routeId.isBlank()) {
                // Let the existing client-side validation set its error state; no point
                // running the driver through permission prompts for an invalid form.
                viewModel.onStartTrip(vehicleId, onTripStarted)
            } else {
                permissionFlow.begin()
            }
        },
    )
}

@Composable
fun TripSetupScreenContent(
    state: TripSetupUiState,
    onRouteIdChange: (String) -> Unit,
    onGtfsTripIdChange: (String) -> Unit,
    onRecentRouteClick: (String) -> Unit,
    onStartTripClick: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize().padding(24.dp)) {
        Text(text = stringResource(R.string.trip_setup_title), style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(24.dp))
        OutlinedTextField(
            value = state.routeId,
            onValueChange = onRouteIdChange,
            label = { Text(stringResource(R.string.trip_route_id_label)) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
        )
        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = state.gtfsTripId,
            onValueChange = onGtfsTripIdChange,
            label = { Text(stringResource(R.string.trip_gtfs_trip_id_label)) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
        )
        if (state.recentRoutes.isNotEmpty()) {
            Spacer(Modifier.height(16.dp))
            Text(stringResource(R.string.trip_recent_routes_label), style = MaterialTheme.typography.labelLarge)
            Spacer(Modifier.height(8.dp))
            LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                items(state.recentRoutes) { route ->
                    OutlinedButton(
                        onClick = { onRecentRouteClick(route) },
                        modifier = Modifier.heightIn(min = 48.dp),
                    ) {
                        Text(route)
                    }
                }
            }
        }
        if (state.error != null) {
            Spacer(Modifier.height(16.dp))
            Text(
                text = stringResource(tripErrorMessageRes(state.error)),
                color = MaterialTheme.colorScheme.error,
            )
        }
        Spacer(Modifier.height(24.dp))
        Button(
            onClick = onStartTripClick,
            enabled = !state.loading,
            modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
        ) {
            Text(stringResource(R.string.trip_start_button))
        }
    }
}

private fun tripErrorMessageRes(error: TripError): Int = when (error) {
    TripError.NOT_ASSIGNED -> R.string.trip_error_not_assigned
    TripError.TRIP_ACTIVE -> R.string.trip_error_trip_active
    TripError.NETWORK -> R.string.trip_error_network
    TripError.OTHER -> R.string.trip_error_other
}
