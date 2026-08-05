package org.onebusaway.vehicletracker.ui.tracking

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.delay
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.ui.theme.StatusGreen
import org.onebusaway.vehicletracker.ui.theme.StatusRed
import java.util.Locale

@Composable
fun TrackingScreen(
    onTripEnded: () -> Unit,
    onReauthRequired: () -> Unit,
    viewModel: TrackingViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    TrackingScreenContent(
        state = state,
        onEndTripClick = { viewModel.onEndTrip(onTripEnded) },
        onEndTripLocallyClick = { viewModel.onEndTripLocally(onTripEnded) },
        onDismissError = viewModel::dismissEndTripError,
        onReauthClick = onReauthRequired,
    )
}

@Composable
fun TrackingScreenContent(
    state: TrackingUiState,
    onEndTripClick: () -> Unit,
    onEndTripLocallyClick: () -> Unit,
    onDismissError: () -> Unit,
    onReauthClick: () -> Unit,
) {
    var showConfirmDialog by remember { mutableStateOf(false) }
    var elapsedSeconds by remember { mutableLongStateOf(0L) }

    val startedAt = state.tracking.tripStartedAtEpochSec ?: state.activeTrip?.startedAtEpochSec
    LaunchedEffect(startedAt) {
        if (startedAt != null) {
            while (true) {
                elapsedSeconds = (System.currentTimeMillis() / 1000) - startedAt
                delay(1000)
            }
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        StatusBanner(
            problem = state.tracking.problem,
            modifier = Modifier.fillMaxWidth().weight(1f),
        )
        Column(
            modifier = Modifier.weight(2f).fillMaxWidth().padding(24.dp),
        ) {
            if (state.tracking.problem == TrackingProblem.AUTH_EXPIRED) {
                Button(
                    onClick = onReauthClick,
                    colors = ButtonDefaults.buttonColors(containerColor = StatusRed),
                    modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
                ) {
                    Text(stringResource(R.string.tracking_reauth_button))
                }
                Spacer(Modifier.height(16.dp))
            }
            state.activeTrip?.let { trip ->
                Text(
                    text = stringResource(R.string.tracking_route_label, trip.routeId),
                    style = MaterialTheme.typography.titleLarge,
                )
                Spacer(Modifier.height(8.dp))
            }
            Text(
                text = stringResource(R.string.tracking_duration_label, formatDuration(elapsedSeconds)),
                style = MaterialTheme.typography.bodyLarge,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = stringResource(R.string.tracking_fixes_sent_label, state.tracking.fixesSent),
                style = MaterialTheme.typography.bodyLarge,
            )
            Spacer(Modifier.weight(1f))
            Button(
                onClick = { showConfirmDialog = true },
                enabled = !state.ending,
                colors = ButtonDefaults.buttonColors(containerColor = StatusRed),
                modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
            ) {
                Text(stringResource(R.string.tracking_end_trip_button))
            }
        }
    }

    if (showConfirmDialog) {
        AlertDialog(
            onDismissRequest = { showConfirmDialog = false },
            title = { Text(stringResource(R.string.tracking_end_trip_dialog_title)) },
            text = { Text(stringResource(R.string.tracking_end_trip_dialog_message)) },
            confirmButton = {
                TextButton(onClick = {
                    showConfirmDialog = false
                    onEndTripClick()
                }) {
                    Text(stringResource(R.string.tracking_end_trip_dialog_confirm))
                }
            },
            dismissButton = {
                TextButton(onClick = { showConfirmDialog = false }) {
                    Text(stringResource(R.string.tracking_end_trip_dialog_cancel))
                }
            },
        )
    }

    if (state.endTripError) {
        AlertDialog(
            onDismissRequest = onDismissError,
            title = { Text(stringResource(R.string.tracking_end_trip_error_title)) },
            text = { Text(stringResource(R.string.tracking_end_trip_error_message)) },
            confirmButton = {
                TextButton(onClick = onEndTripClick) {
                    Text(stringResource(R.string.tracking_end_trip_error_retry))
                }
            },
            dismissButton = {
                TextButton(onClick = onEndTripLocallyClick) {
                    Text(stringResource(R.string.tracking_end_trip_error_end_locally))
                }
            },
        )
    }
}

@Composable
private fun StatusBanner(problem: TrackingProblem, modifier: Modifier = Modifier) {
    val (backgroundColor, textRes) = when (problem) {
        TrackingProblem.NO_NETWORK -> StatusRed to R.string.tracking_status_no_network
        TrackingProblem.NO_GPS -> StatusRed to R.string.tracking_status_no_gps
        TrackingProblem.CLOCK_SKEW -> StatusRed to R.string.tracking_status_clock_skew
        TrackingProblem.AUTH_EXPIRED -> StatusRed to R.string.tracking_status_auth_expired
        TrackingProblem.NONE -> StatusGreen to R.string.tracking_status_connected
    }
    Box(
        modifier = modifier.background(backgroundColor),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = stringResource(textRes),
            color = Color.White,
            fontSize = 32.sp,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(16.dp),
        )
    }
}

private fun formatDuration(totalSeconds: Long): String {
    val s = totalSeconds.coerceAtLeast(0)
    val hours = s / 3600
    val minutes = (s % 3600) / 60
    val seconds = s % 60
    return if (hours > 0) {
        String.format(Locale.getDefault(), "%d:%02d:%02d", hours, minutes, seconds)
    } else {
        String.format(Locale.getDefault(), "%02d:%02d", minutes, seconds)
    }
}
