package org.onebusaway.vehicletracker.ui.vehicles

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R

@Composable
fun VehicleScreen(
    onVehicleSelected: (String) -> Unit,
    viewModel: VehicleViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    LaunchedEffect(state) {
        val loaded = state as? VehiclesUiState.Loaded
        if (loaded != null && loaded.vehicles.size == 1) {
            onVehicleSelected(loaded.vehicles.first().id)
        }
    }

    VehicleScreenContent(
        state = state,
        onVehicleClick = onVehicleSelected,
        onRetry = viewModel::retry,
    )
}

@Composable
fun VehicleScreenContent(
    state: VehiclesUiState,
    onVehicleClick: (String) -> Unit,
    onRetry: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        Text(text = stringResource(R.string.vehicles_title), style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(16.dp))
        when (state) {
            is VehiclesUiState.Loading -> {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            }
            is VehiclesUiState.Loaded -> {
                if (state.vehicles.isEmpty()) {
                    Text(stringResource(R.string.vehicles_empty))
                } else {
                    LazyColumn {
                        items(state.vehicles) { vehicle ->
                            Button(
                                onClick = { onVehicleClick(vehicle.id) },
                                modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp).padding(vertical = 4.dp),
                            ) {
                                Text(vehicle.label)
                            }
                        }
                    }
                }
            }
            is VehiclesUiState.Error -> {
                Text(stringResource(R.string.vehicles_error_message))
                if (state.retry) {
                    Spacer(Modifier.height(16.dp))
                    Button(
                        onClick = onRetry,
                        modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
                    ) {
                        Text(stringResource(R.string.vehicles_retry_button))
                    }
                }
            }
        }
    }
}
