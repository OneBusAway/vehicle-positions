package org.onebusaway.vehicletracker.ui.vehicles

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconToggleButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.api.VehicleDto

@Composable
fun VehicleScreen(
    onVehicleSelected: (String) -> Unit,
    viewModel: VehicleViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    // Keyed on the id rather than the whole state so typing in the search field does not re-run
    // the effect. VehiclesUiState.Loaded.autoSelectId reads the unfiltered list for the same
    // reason: narrowing a search to one match must not navigate the driver away.
    val autoSelectId = (state as? VehiclesUiState.Loaded)?.autoSelectId
    LaunchedEffect(autoSelectId) {
        if (autoSelectId != null) onVehicleSelected(autoSelectId)
    }

    VehicleScreenContent(
        state = state,
        onVehicleClick = onVehicleSelected,
        onQueryChange = viewModel::onQueryChange,
        onToggleFavorite = viewModel::onToggleFavorite,
        onRetry = viewModel::retry,
    )
}

@Composable
fun VehicleScreenContent(
    state: VehiclesUiState,
    onVehicleClick: (String) -> Unit,
    onQueryChange: (String) -> Unit,
    onToggleFavorite: (String) -> Unit,
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
                    OutlinedTextField(
                        value = state.query,
                        onValueChange = onQueryChange,
                        label = { Text(stringResource(R.string.vehicles_search_label)) },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
                    )
                    Spacer(Modifier.height(12.dp))
                    if (state.visible.isEmpty()) {
                        Text(stringResource(R.string.vehicles_search_no_matches))
                    } else {
                        LazyColumn {
                            items(state.visible, key = { it.id }) { vehicle ->
                                VehicleRow(
                                    vehicle = vehicle,
                                    favorite = vehicle.id in state.favorites,
                                    onClick = { onVehicleClick(vehicle.id) },
                                    onToggleFavorite = { onToggleFavorite(vehicle.id) },
                                )
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

@Composable
private fun VehicleRow(
    vehicle: VehicleDto,
    favorite: Boolean,
    onClick: () -> Unit,
    onToggleFavorite: () -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Button(
            onClick = onClick,
            modifier = Modifier.weight(1f).heightIn(min = 64.dp),
        ) {
            Text(vehicle.label)
        }
        Spacer(Modifier.width(8.dp))
        IconToggleButton(
            checked = favorite,
            onCheckedChange = { onToggleFavorite() },
            modifier = Modifier.heightIn(min = 48.dp),
        ) {
            Icon(
                painter = painterResource(if (favorite) R.drawable.ic_star_filled else R.drawable.ic_star_outline),
                // The control is icon-only, so the description is what TalkBack announces —
                // it names the vehicle and the action the tap performs, not the icon.
                contentDescription = stringResource(
                    if (favorite) R.string.vehicles_unfavorite_description else R.string.vehicles_favorite_description,
                    vehicle.label,
                ),
            )
        }
    }
}
