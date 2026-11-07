package org.onebusaway.vehicletracker.ui.vehicles

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconToggleButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.DelayedSpinner
import org.onebusaway.vehicletracker.ui.components.EmptyState
import org.onebusaway.vehicletracker.ui.components.SearchField
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.fleetNumber

private val TileShape = RoundedCornerShape(20.dp)
private val TileGap = 12.dp

// The route and run steps have a 56 dp header over their title; the bus step has none, but keeps its title at the
// same height so the title does not jump as the screens slide.
private val TitleTop = 64.dp

/**
 * [onVehicleSelected] is a tap on a tile. [onOnlyVehicle] is the bus a driver with one assignment never has to
 * choose, and takes the bus step's place: navigating there leaves nothing behind it that would skip forward again.
 * [onSessionEnded] is the server having ended the session.
 */
@Composable
fun VehicleScreen(
    onVehicleSelected: (String) -> Unit,
    onOnlyVehicle: (String) -> Unit,
    onSessionEnded: () -> Unit,
    viewModel: VehicleViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    // Keyed on the id rather than the whole state so typing in the search field does not re-run
    // the effect. VehiclesUiState.Loaded.autoSelectId reads the unfiltered list for the same
    // reason: narrowing a search to one match must not navigate the driver away.
    val autoSelectId = (state as? VehiclesUiState.Loaded)?.autoSelectId
    LaunchedEffect(autoSelectId) {
        if (autoSelectId != null) onOnlyVehicle(autoSelectId)
    }

    VehicleScreenContent(
        state = state,
        onVehicleClick = onVehicleSelected,
        onQueryChange = viewModel::onQueryChange,
        onToggleFavorite = viewModel::onToggleFavorite,
        onRetry = viewModel::retry,
        onSessionEnded = onSessionEnded,
    )
}

/**
 * The bus step: the driver's buses as big tiles, two to a row, under Favorites, Recent and All buses, or under
 * Matches while searching. Title, search, labels and tiles are all items of one grid, so the page scrolls as one.
 */
@Composable
fun VehicleScreenContent(
    state: VehiclesUiState,
    onVehicleClick: (String) -> Unit,
    onQueryChange: (String) -> Unit,
    onToggleFavorite: (String) -> Unit,
    onRetry: () -> Unit,
    onSessionEnded: () -> Unit,
) {
    val sessionEnded = state is VehiclesUiState.SessionEnded
    LaunchedEffect(sessionEnded) { if (sessionEnded) onSessionEnded() }

    LazyVerticalGrid(
        columns = GridCells.Fixed(2),
        state = rememberLazyGridState(),
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 24.dp),
        horizontalArrangement = Arrangement.spacedBy(TileGap),
    ) {
        fullWidth("title") {
            Text(
                text = stringResource(R.string.vehicles_title),
                style = MaterialTheme.typography.headlineLarge,
                modifier = Modifier.padding(start = 8.dp, top = TitleTop, bottom = 18.dp).semantics { heading() },
            )
        }
        when (state) {
            is VehiclesUiState.Loading, is VehiclesUiState.SessionEnded -> fullWidth("progress") { DelayedSpinner() }
            is VehiclesUiState.Loaded -> loaded(state, onVehicleClick, onQueryChange, onToggleFavorite, onRetry)
            is VehiclesUiState.Error -> fullWidth("empty") {
                EmptyState(
                    icon = R.drawable.ic_cloud_off,
                    title = stringResource(R.string.vehicles_error_message),
                    body = stringResource(R.string.vehicles_error_detail),
                    action = stringResource(R.string.vehicles_retry_button),
                    onAction = onRetry,
                )
            }
        }
    }
}

private fun LazyGridScope.loaded(
    state: VehiclesUiState.Loaded,
    onVehicleClick: (String) -> Unit,
    onQueryChange: (String) -> Unit,
    onToggleFavorite: (String) -> Unit,
    onRetry: () -> Unit,
) {
    if (state.vehicles.isEmpty()) {
        fullWidth("empty") {
            EmptyState(
                icon = R.drawable.ic_directions_bus,
                title = stringResource(R.string.vehicles_empty),
                body = stringResource(R.string.vehicles_empty_detail),
                action = stringResource(R.string.vehicles_check_again),
                onAction = onRetry,
            )
        }
        return
    }
    fullWidth("search") {
        SearchField(
            query = state.query,
            onQueryChange = onQueryChange,
            placeholder = stringResource(R.string.vehicles_search_label),
            modifier = Modifier.padding(bottom = 4.dp),
        )
    }
    if (state.groups.isEmpty()) {
        fullWidth("empty") {
            EmptyState(
                icon = R.drawable.ic_search_off,
                title = stringResource(R.string.vehicles_search_no_matches, state.query),
                body = stringResource(R.string.vehicles_search_no_matches_detail),
            )
        }
        return
    }
    for (group in state.groups) {
        item(key = "label:${group.section}", span = { GridItemSpan(maxLineSpan) }, contentType = "label") {
            Text(
                text = stringResource(sectionLabel(group.section)),
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier
                    .animateItem()
                    .padding(start = 8.dp, top = 18.dp, bottom = 10.dp)
                    .semantics { heading() },
            )
        }
        items(group.tiles, key = { "bus:${it.id}" }, contentType = { "tile" }) { tile ->
            BusTileView(
                tile = tile,
                onClick = onVehicleClick,
                onToggleFavorite = onToggleFavorite,
                modifier = Modifier.animateItem().padding(bottom = TileGap),
            )
        }
    }
}

private fun LazyGridScope.fullWidth(key: String, content: @Composable () -> Unit) {
    item(key = key, span = { GridItemSpan(maxLineSpan) }, contentType = key) { content() }
}

private fun sectionLabel(section: BusSection): Int = when (section) {
    BusSection.FAVORITES -> R.string.vehicles_section_favorites
    BusSection.RECENT -> R.string.vehicles_section_recent
    BusSection.ALL -> R.string.vehicles_section_all
    BusSection.MATCHES -> R.string.vehicles_section_matches
}

/**
 * One bus: its fleet number big, the label under it, and the star in the corner. The tile is one button, read as
 * "4317, Bus 4317, Favorite"; the star keeps its own toggle.
 */
@Composable
private fun BusTileView(
    tile: BusTile,
    onClick: (String) -> Unit,
    onToggleFavorite: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val favoriteState = stringResource(R.string.vehicles_tile_favorite)
    Box(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = 96.dp)
            .clip(TileShape)
            .background(MaterialTheme.colorScheme.surfaceContainer)
            .clickable(role = Role.Button) { onClick(tile.id) }
            .semantics { if (tile.favorite) stateDescription = favoriteState },
    ) {
        Column(
            modifier = Modifier.align(Alignment.BottomStart).padding(start = 18.dp, top = 14.dp, end = 18.dp, bottom = 14.dp),
        ) {
            Text(
                text = tile.id,
                style = MaterialTheme.typography.fleetNumber,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                // Clear of the star, which sits over the end of this line on a short tile.
                modifier = Modifier.padding(end = 30.dp),
            )
            if (tile.label != null) {
                Text(
                    text = tile.label,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        IconToggleButton(
            checked = tile.favorite,
            onCheckedChange = { onToggleFavorite(tile.id) },
            modifier = Modifier.align(Alignment.TopEnd),
        ) {
            Icon(
                painter = painterResource(if (tile.favorite) R.drawable.ic_star_filled else R.drawable.ic_star_outline),
                // The control is icon-only, so the description is what TalkBack announces: it names
                // the bus and the action the tap performs, not the icon.
                contentDescription = stringResource(
                    if (tile.favorite) R.string.vehicles_unfavorite_description else R.string.vehicles_favorite_description,
                    tile.id,
                ),
                tint = if (tile.favorite) AppTheme.problemColors.iconOnSurface else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
