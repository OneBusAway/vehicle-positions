package org.onebusaway.vehicletracker.ui.routes

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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.api.RouteDto

@Composable
fun RoutesScreen(
    onRouteSelected: (String) -> Unit,
    viewModel: RoutesViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    RoutesScreenContent(
        state = state,
        onRouteClick = onRouteSelected,
        onQueryChange = viewModel::onQueryChange,
        onRetry = viewModel::retry,
    )
}

@Composable
fun RoutesScreenContent(
    state: RoutesUiState,
    onRouteClick: (String) -> Unit,
    onQueryChange: (String) -> Unit,
    onRetry: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        Text(text = stringResource(R.string.routes_title), style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(16.dp))
        when (state) {
            is RoutesUiState.Loading -> {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            }
            is RoutesUiState.Loaded -> {
                if (state.routes.isEmpty()) {
                    Text(stringResource(R.string.routes_empty))
                } else {
                    OutlinedTextField(
                        value = state.query,
                        onValueChange = onQueryChange,
                        label = { Text(stringResource(R.string.routes_search_label)) },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
                    )
                    Spacer(Modifier.height(12.dp))
                    if (state.visible.isEmpty()) {
                        Text(stringResource(R.string.routes_search_no_matches))
                    } else {
                        RouteList(state = state, onRouteClick = onRouteClick)
                    }
                }
            }
            is RoutesUiState.Error -> {
                Text(stringResource(R.string.routes_error_message))
                if (state.retry) {
                    Spacer(Modifier.height(16.dp))
                    Button(
                        onClick = onRetry,
                        modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp),
                    ) {
                        Text(stringResource(R.string.routes_retry_button))
                    }
                }
            }
        }
    }
}

@Composable
private fun RouteList(state: RoutesUiState.Loaded, onRouteClick: (String) -> Unit) {
    LazyColumn {
        if (state.recent.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.routes_section_recent)) }
            // Keys are prefixed because a recently driven route also appears in the section
            // below it, and two items in one list cannot share a key.
            items(state.recent, key = { "recent-${it.id}" }) { route ->
                RouteRow(route = route, onClick = { onRouteClick(route.id) })
            }
        }
        item {
            SectionHeader(
                stringResource(
                    if (state.query.isEmpty()) R.string.routes_section_all else R.string.routes_section_matches,
                ),
            )
        }
        items(state.visible, key = { it.id }) { route ->
            RouteRow(route = route, onClick = { onRouteClick(route.id) })
        }
    }
}

@Composable
private fun SectionHeader(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        modifier = Modifier.padding(top = 12.dp, bottom = 4.dp),
    )
}

@Composable
private fun RouteRow(route: RouteDto, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 64.dp)
            .clickable(onClick = onClick)
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RouteBadge(route)
        Spacer(Modifier.width(12.dp))
        Text(
            text = route.longName,
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
