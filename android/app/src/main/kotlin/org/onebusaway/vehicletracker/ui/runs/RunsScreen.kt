package org.onebusaway.vehicletracker.ui.runs

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFlow
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

@Composable
fun RunsScreen(
    onTripStarted: () -> Unit,
    viewModel: RunsViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    // The permission sequence has no argument to carry the tapped run through, so the run waits
    // here until it comes back ready.
    var pendingRunId by rememberSaveable { mutableStateOf<String?>(null) }
    val permissionFlow = rememberPermissionFlow(
        onReady = { pendingRunId?.let { viewModel.onStartRun(it, onTripStarted) } },
    )

    val listState = rememberLazyListState()
    val loaded = state as? RunsUiState.Loaded
    val highlightedIndex = loaded?.page?.runs?.indexOfFirst { it.id == loaded.highlightedId } ?: -1
    LaunchedEffect(highlightedIndex) {
        if (highlightedIndex >= 0) listState.animateScrollToItem(highlightedIndex)
    }

    RunsScreenContent(
        state = state,
        listState = listState,
        onRunClick = { runId ->
            pendingRunId = runId
            permissionFlow.begin()
        },
        onRetry = viewModel::retry,
    )
}

@Composable
fun RunsScreenContent(
    state: RunsUiState,
    listState: LazyListState,
    onRunClick: (String) -> Unit,
    onRetry: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        Text(text = stringResource(R.string.runs_title), style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(16.dp))
        when (state) {
            is RunsUiState.Loading -> {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            }
            is RunsUiState.Loaded -> {
                Text(serviceDateLabel(state.page.serviceDate), style = MaterialTheme.typography.labelLarge)
                if (state.starting) {
                    Spacer(Modifier.height(8.dp))
                    StartingRow()
                }
                if (state.error != null) {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = stringResource(runsErrorMessageRes(state.error)),
                        color = MaterialTheme.colorScheme.error,
                    )
                    // The list itself is out of date, so reloading it is what helps, not the same tap again.
                    if (state.error == TripError.TRIP_NOT_ACTIVE) {
                        Spacer(Modifier.height(8.dp))
                        Button(
                            onClick = onRetry,
                            modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
                        ) {
                            Text(stringResource(R.string.runs_reload_button))
                        }
                    }
                }
                Spacer(Modifier.height(12.dp))
                if (state.page.runs.isEmpty()) {
                    Text(stringResource(R.string.runs_empty))
                } else {
                    LazyColumn(state = listState) {
                        items(state.page.runs, key = { it.id }) { run ->
                            RunRow(
                                run = run,
                                zone = state.page.timezone,
                                highlight = if (run.id == state.highlightedId) state.highlight else null,
                                enabled = !state.starting,
                                onClick = { onRunClick(run.id) },
                            )
                        }
                    }
                }
            }
            is RunsUiState.Error -> {
                Text(stringResource(R.string.runs_error_message))
                if (state.retry) {
                    Spacer(Modifier.height(16.dp))
                    Button(
                        onClick = onRetry,
                        modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
                    ) {
                        Text(stringResource(R.string.runs_retry_button))
                    }
                }
            }
        }
    }
}

@Composable
private fun StartingRow() {
    Row(verticalAlignment = Alignment.CenterVertically) {
        CircularProgressIndicator(Modifier.size(20.dp))
        Spacer(Modifier.width(8.dp))
        Text(stringResource(R.string.runs_starting))
    }
}

@Composable
private fun RunRow(
    run: TripRun,
    zone: ZoneId,
    highlight: RunHighlight?,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    val clock = remember { DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 72.dp)
            .clickable(enabled = enabled, onClick = onClick)
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = stringResource(
                    R.string.runs_row_headsign,
                    clock.format(clockTime(run.startsAt, zone)),
                    run.headsign,
                ),
                style = MaterialTheme.typography.titleMedium,
            )
            Text(
                text = stringResource(R.string.runs_row_stops, run.firstStop, run.lastStop),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (highlight != null) {
            Spacer(Modifier.width(8.dp))
            HighlightBadge(highlight)
        }
    }
}

@Composable
private fun HighlightBadge(highlight: RunHighlight) {
    Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = RoundedCornerShape(12.dp)) {
        Text(
            text = stringResource(
                when (highlight) {
                    RunHighlight.NOW -> R.string.runs_badge_now
                    RunHighlight.NEXT -> R.string.runs_badge_next
                },
            ),
            color = MaterialTheme.colorScheme.onPrimaryContainer,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 6.dp),
        )
    }
}

private fun runsErrorMessageRes(error: TripError): Int = when (error) {
    TripError.NOT_ASSIGNED -> R.string.runs_error_not_assigned
    TripError.TRIP_ACTIVE -> R.string.runs_error_trip_active
    TripError.NETWORK -> R.string.runs_error_network
    TripError.NO_GEOMETRY -> R.string.runs_error_no_geometry
    TripError.TRIP_NOT_ACTIVE -> R.string.runs_error_trip_not_active
    TripError.OTHER -> R.string.runs_error_other
}
