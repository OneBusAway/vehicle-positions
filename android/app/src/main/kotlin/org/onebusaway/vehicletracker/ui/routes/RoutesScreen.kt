package org.onebusaway.vehicletracker.ui.routes

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.account.AccountMenu
import org.onebusaway.vehicletracker.ui.components.DelayedSpinner
import org.onebusaway.vehicletracker.ui.components.EmptyState
import org.onebusaway.vehicletracker.ui.components.ScrolledUnderDivider
import org.onebusaway.vehicletracker.ui.components.SearchField
import org.onebusaway.vehicletracker.ui.components.SetupHeader

private const val GRID_COLUMNS = 4
private val BadgeGap = 10.dp
private val BigBadgeShape = RoundedCornerShape(14.dp)
private val RowBadgeShape = RoundedCornerShape(10.dp)

/**
 * [onBack] and [onBusClick] are null when the bus step was skipped: there is no bus step to go back to.
 * [onSessionEnded] is the server having ended the session, and [onSignedOut] the driver having signed out.
 */
@Composable
fun RoutesScreen(
    busId: String,
    onBack: (() -> Unit)?,
    onBusClick: (() -> Unit)?,
    onRouteSelected: (RouteItem) -> Unit,
    onSessionEnded: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: RoutesViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    RoutesScreenContent(
        state = state,
        busId = busId,
        onBack = onBack,
        onBusClick = onBusClick,
        onRouteClick = onRouteSelected,
        onQueryChange = viewModel::onQueryChange,
        onRetry = viewModel::retry,
        onSessionEnded = onSessionEnded,
        account = { AccountMenu(onSignedOut) },
    )
}

/**
 * The route step: the routes the driver drove last as big badges, then every route as a row with its name, or as a
 * grid of badges when the feed names none of them. Under the header with the trip so far, everything is an item of
 * one grid, so the page scrolls as one.
 */
@Composable
fun RoutesScreenContent(
    state: RoutesUiState,
    busId: String,
    onBack: (() -> Unit)?,
    onBusClick: (() -> Unit)?,
    onRouteClick: (RouteItem) -> Unit,
    onQueryChange: (String) -> Unit,
    onRetry: () -> Unit,
    onSessionEnded: () -> Unit,
    account: @Composable () -> Unit,
) {
    val sessionEnded = state is RoutesUiState.SessionEnded
    LaunchedEffect(sessionEnded) { if (sessionEnded) onSessionEnded() }

    val gridState = rememberLazyGridState()
    Column(Modifier.fillMaxSize()) {
        SetupHeader(
            busId = busId,
            step = stringResource(R.string.setup_step_route),
            onBack = onBack,
            onBusClick = onBusClick,
            account = account,
        )
        ScrolledUnderDivider({ gridState.canScrollBackward })
        RouteGrid(state, gridState, onRouteClick, onQueryChange, onRetry)
    }
}

@Composable
private fun RouteGrid(
    state: RoutesUiState,
    gridState: LazyGridState,
    onRouteClick: (RouteItem) -> Unit,
    onQueryChange: (String) -> Unit,
    onRetry: () -> Unit,
) {
    LazyVerticalGrid(
        columns = GridCells.Fixed(GRID_COLUMNS),
        state = gridState,
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 24.dp),
        horizontalArrangement = Arrangement.spacedBy(BadgeGap),
    ) {
        fullWidth("title") {
            Text(
                text = stringResource(R.string.routes_title),
                style = MaterialTheme.typography.headlineLarge,
                modifier = Modifier.padding(start = 8.dp, top = 8.dp, bottom = 18.dp).semantics { heading() },
            )
        }
        when (state) {
            is RoutesUiState.Loading, is RoutesUiState.SessionEnded -> fullWidth("progress") { DelayedSpinner() }
            is RoutesUiState.Loaded -> loaded(state, onRouteClick, onQueryChange, onRetry)
            is RoutesUiState.NoSchedule -> fullWidth("empty") {
                EmptyState(
                    icon = R.drawable.ic_calendar_month,
                    title = stringResource(R.string.routes_no_schedule),
                    body = stringResource(R.string.routes_no_schedule_detail),
                )
            }
            is RoutesUiState.Error -> fullWidth("empty") {
                EmptyState(
                    icon = R.drawable.ic_cloud_off,
                    title = stringResource(R.string.routes_error_message),
                    body = stringResource(R.string.routes_error_detail),
                    action = stringResource(R.string.routes_retry_button),
                    onAction = onRetry,
                )
            }
        }
    }
}

private fun LazyGridScope.loaded(
    state: RoutesUiState.Loaded,
    onRouteClick: (RouteItem) -> Unit,
    onQueryChange: (String) -> Unit,
    onRetry: () -> Unit,
) {
    if (state.routes.isEmpty()) {
        fullWidth("empty") {
            EmptyState(
                icon = R.drawable.ic_event_busy,
                title = stringResource(R.string.routes_empty),
                body = stringResource(R.string.routes_empty_detail),
                action = stringResource(R.string.routes_check_again),
                onAction = onRetry,
            )
        }
        return
    }
    fullWidth("search") {
        SearchField(
            query = state.query,
            onQueryChange = onQueryChange,
            placeholder = stringResource(R.string.routes_search_label),
            modifier = Modifier.padding(bottom = 4.dp),
        )
    }
    if (state.visible.isEmpty()) {
        fullWidth("empty") {
            EmptyState(
                icon = R.drawable.ic_search_off,
                title = stringResource(R.string.routes_search_no_matches, state.query),
                body = stringResource(R.string.routes_search_no_matches_detail),
            )
        }
        return
    }
    if (state.recent.isNotEmpty()) {
        label("label:recent") { stringResource(R.string.routes_section_recent) }
        item(key = "recent", span = { GridItemSpan(maxLineSpan) }, contentType = "recent") {
            FlowRow(
                modifier = Modifier.animateItem(),
                horizontalArrangement = Arrangement.spacedBy(BadgeGap),
                verticalArrangement = Arrangement.spacedBy(BadgeGap),
            ) {
                for (route in state.recent) {
                    BigBadge(route, onRouteClick, Modifier.defaultMinSize(minWidth = 64.dp))
                }
            }
        }
    }
    label("label:all") {
        when {
            state.query.isNotEmpty() -> stringResource(R.string.routes_section_matches)
            // A grid of bare numbers has no names to scan, so its label says how many there are.
            state.grid -> pluralStringResource(R.plurals.routes_section_all_count, state.routes.size, state.routes.size)
            else -> stringResource(R.string.routes_section_all)
        }
    }
    if (state.grid) {
        items(state.visible, key = { "route:${it.id}" }, contentType = { "badge" }) { route ->
            BigBadge(route, onRouteClick, Modifier.animateItem().fillMaxWidth().padding(bottom = BadgeGap))
        }
    } else {
        items(state.visible, key = { "route:${it.id}" }, span = { GridItemSpan(maxLineSpan) }, contentType = { "row" }) { route ->
            RouteRow(route, onRouteClick, Modifier.animateItem())
        }
    }
}

private fun LazyGridScope.fullWidth(key: String, content: @Composable () -> Unit) {
    item(key = key, span = { GridItemSpan(maxLineSpan) }, contentType = key) { content() }
}

private fun LazyGridScope.label(key: String, text: @Composable () -> String) {
    item(key = key, span = { GridItemSpan(maxLineSpan) }, contentType = "label") {
        Text(
            text = text(),
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier
                .animateItem()
                .padding(start = 8.dp, top = 18.dp, bottom = 10.dp)
                .semantics { heading() },
        )
    }
}

/** A route as a big badge alone, in the Recent row and the grid; TalkBack reads it as "Route 7". */
@Composable
private fun BigBadge(route: RouteItem, onRouteClick: (RouteItem) -> Unit, modifier: Modifier = Modifier) {
    val description = stringResource(R.string.setup_route, route.badge.shortName)
    RouteBadge(
        route = route.badge,
        modifier = modifier
            .height(56.dp)
            .clip(BigBadgeShape)
            .clickable(role = Role.Button) { onRouteClick(route) }
            .clearAndSetSemantics {
                contentDescription = description
                role = Role.Button
                onClick {
                    onRouteClick(route)
                    true
                }
            },
        style = MaterialTheme.typography.titleLarge,
        shape = BigBadgeShape,
    )
}

/** A route as a row: its badge, then its long name on one line. */
@Composable
private fun RouteRow(route: RouteItem, onRouteClick: (RouteItem) -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = 60.dp)
            .clip(RowBadgeShape)
            .clickable(role = Role.Button) { onRouteClick(route) }
            .padding(horizontal = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RouteBadge(
            route = route.badge,
            modifier = Modifier.defaultMinSize(minWidth = 56.dp, minHeight = 40.dp),
            shape = RowBadgeShape,
        )
        Text(
            text = route.longName,
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
