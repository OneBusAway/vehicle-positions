package org.onebusaway.vehicletracker.ui.resume

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.theme.StatusRed
import org.onebusaway.vehicletracker.ui.tracking.EndTripErrorDialog

@Composable
fun ResumeShiftScreen(
    onResumed: () -> Unit,
    onEnded: () -> Unit,
    viewModel: ResumeShiftViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    ResumeShiftScreenContent(
        state = state,
        onResumeClick = { viewModel.onResumeShift(onResumed) },
        onEndShiftClick = { viewModel.onEndShift(onEnded) },
        onEndShiftLocallyClick = { viewModel.onEndShiftLocally(onEnded) },
        onDismissError = viewModel::dismissEndShiftError,
    )
}

@Composable
fun ResumeShiftScreenContent(
    state: ResumeShiftUiState,
    onResumeClick: () -> Unit,
    onEndShiftClick: () -> Unit,
    onEndShiftLocallyClick: () -> Unit,
    onDismissError: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = stringResource(R.string.resume_title),
            style = MaterialTheme.typography.headlineMedium,
        )
        state.trip?.let { trip ->
            Spacer(Modifier.height(16.dp))
            Text(
                text = stringResource(R.string.resume_message, trip.vehicleId),
                style = MaterialTheme.typography.bodyLarge,
            )
            Spacer(Modifier.height(16.dp))
            Text(
                text = stringResource(R.string.resume_route, trip.routeId),
                style = MaterialTheme.typography.titleLarge,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = startedAgoText(state.startedAgoSec),
                style = MaterialTheme.typography.bodyLarge,
            )
        }
        Spacer(Modifier.height(32.dp))
        Button(
            onClick = onResumeClick,
            enabled = !state.ending,
            modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
        ) {
            Text(stringResource(R.string.resume_button))
        }
        Spacer(Modifier.height(16.dp))
        Button(
            onClick = onEndShiftClick,
            enabled = !state.ending,
            colors = ButtonDefaults.buttonColors(containerColor = StatusRed),
            modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
        ) {
            Text(stringResource(R.string.resume_end_shift_button))
        }
    }

    if (state.endTripError) {
        EndTripErrorDialog(onRetry = onEndShiftClick, onEndLocally = onEndShiftLocallyClick, onDismiss = onDismissError)
    }
}

/** Whole minutes, as hours and minutes once past the hour: a shift is recognised by roughly when it began. */
@Composable
private fun startedAgoText(seconds: Long): String {
    val minutes = (seconds / 60).toInt()
    return if (minutes < 60) {
        pluralStringResource(R.plurals.resume_started_minutes_ago, minutes, minutes)
    } else {
        stringResource(R.string.resume_started_hours_ago, minutes / 60, minutes % 60)
    }
}
