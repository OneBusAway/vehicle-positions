package org.onebusaway.vehicletracker.ui.runs

import android.text.format.DateFormat
import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleStartEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.AttentionBanner
import org.onebusaway.vehicletracker.ui.components.DelayedSpinner
import org.onebusaway.vehicletracker.ui.components.EmptyState
import org.onebusaway.vehicletracker.ui.components.ScrolledUnderDivider
import org.onebusaway.vehicletracker.ui.components.SetupHeader
import org.onebusaway.vehicletracker.ui.permissions.PreflightContent
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFlow
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.departureTime
import org.onebusaway.vehicletracker.ui.theme.fadeThroughIn
import org.onebusaway.vehicletracker.ui.theme.fadeThroughOut
import java.time.LocalDate
import java.time.format.DateTimeFormatter

private val TagShape = RoundedCornerShape(8.dp)

/** [onBusClick] is null when the bus step was skipped: there is no bus step to go back to. */
@Composable
fun RunsScreen(
    onTripStarted: () -> Unit,
    onBack: () -> Unit,
    onBusClick: (() -> Unit)?,
    onRouteClick: () -> Unit,
    onChangeBus: () -> Unit,
    onChooseAnotherRoute: () -> Unit,
    viewModel: RunsViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    LifecycleStartEffect(viewModel) {
        viewModel.onScreenVisible(true)
        onStopOrDispose { viewModel.onScreenVisible(false) }
    }

    // The permission sequence has no argument to carry the tapped run through, so the run waits
    // here until it comes back ready. Try again on a refused start reuses it.
    var pendingRunId by rememberSaveable { mutableStateOf<String?>(null) }
    val permissionFlow = rememberPermissionFlow(
        onReady = { pendingRunId?.let { viewModel.onStartRun(it, onTripStarted) } },
    )
    BackHandler(enabled = permissionFlow.visible) { permissionFlow.cancel() }

    Box {
        // The board stays under the checklist, so its direction and place are as the driver left them, but TalkBack
        // does not reach it there.
        Box(if (permissionFlow.visible) Modifier.clearAndSetSemantics {} else Modifier) {
            RunsScreenContent(
                state = state,
                route = viewModel.route,
                vehicleId = viewModel.vehicleId,
                onBack = onBack,
                onBusClick = onBusClick,
                onRouteClick = onRouteClick,
                onRunClick = { runId ->
                    pendingRunId = runId
                    permissionFlow.start()
                },
                onReload = viewModel::retry,
                onTryAgain = { pendingRunId?.let { viewModel.onStartRun(it, onTripStarted) } },
                onChangeBus = onChangeBus,
                onChooseAnotherRoute = onChooseAnotherRoute,
            )
        }
        AnimatedVisibility(visible = permissionFlow.visible, enter = fadeThroughIn(), exit = fadeThroughOut()) {
            PreflightContent(
                vehicleId = viewModel.vehicleId,
                rows = permissionFlow.checklist,
                canContinue = permissionFlow.canContinue,
                onBack = permissionFlow::cancel,
                onContinue = permissionFlow::onContinue,
                onRowAction = permissionFlow::onRowAction,
            )
        }
    }
}

/**
 * The run step as a departure board: big start times, a switch between the route's two directions, a line at "now",
 * and the run under way or next tagged. The header, the title, the switch and the date stay put; only the board
 * scrolls, and it opens at the run just gone so the driver sees where the day is without scrolling.
 */
@Composable
fun RunsScreenContent(
    state: RunsUiState,
    route: RouteBadgeModel,
    vehicleId: String,
    onBack: () -> Unit,
    onBusClick: (() -> Unit)?,
    onRouteClick: () -> Unit,
    onRunClick: (String) -> Unit,
    onReload: () -> Unit,
    onTryAgain: () -> Unit,
    onChangeBus: () -> Unit,
    onChooseAnotherRoute: () -> Unit,
) {
    Column(Modifier.fillMaxSize()) {
        SetupHeader(
            busId = vehicleId,
            step = stringResource(R.string.setup_step_run),
            onBack = onBack,
            onBusClick = onBusClick,
            route = route,
            onRouteClick = onRouteClick,
        )
        Text(
            text = stringResource(R.string.runs_title),
            style = MaterialTheme.typography.headlineLarge,
            modifier = Modifier.padding(start = 24.dp, top = 8.dp, end = 24.dp, bottom = 18.dp).semantics { heading() },
        )
        when (state) {
            is RunsUiState.Loading -> DelayedSpinner()
            is RunsUiState.Error -> Column(Modifier.verticalScroll(rememberScrollState())) {
                EmptyState(
                    icon = R.drawable.ic_cloud_off,
                    title = stringResource(R.string.runs_error_message),
                    // A 401 is not fixed by trying again: there is nothing for the driver to do here.
                    body = if (state.retry) stringResource(R.string.runs_error_detail) else null,
                    action = if (state.retry) stringResource(R.string.runs_retry_button) else null,
                    onAction = onReload,
                )
            }
            is RunsUiState.Loaded -> if (state.page.runs.isEmpty()) {
                Column(Modifier.verticalScroll(rememberScrollState())) {
                    EmptyState(
                        icon = R.drawable.ic_event_busy,
                        title = if (route.shortName.isBlank()) {
                            stringResource(R.string.runs_empty_unnamed)
                        } else {
                            stringResource(R.string.runs_empty, route.shortName)
                        },
                        body = stringResource(R.string.runs_empty_detail),
                        action = stringResource(R.string.runs_choose_another_route),
                        onAction = onChooseAnotherRoute,
                    )
                }
            } else {
                Departures(state, vehicleId, onRunClick, onReload, onTryAgain, onChangeBus)
            }
        }
    }
}

@Composable
private fun Departures(
    state: RunsUiState.Loaded,
    vehicleId: String,
    onRunClick: (String) -> Unit,
    onReload: () -> Unit,
    onTryAgain: () -> Unit,
    onChangeBus: () -> Unit,
) {
    var savedBoard by rememberSaveable { mutableIntStateOf(state.startBoard) }
    // After the app is killed and restored, the runs are loaded again and can split into fewer boards than when the
    // choice was saved: on a new service day only one direction may run.
    val selected = savedBoard.coerceIn(state.boards.indices)
    // One list state per direction, each made already at its board's opening run, so the board appears there rather
    // than at the top and then scrolls; and switching back finds each direction where the driver left it.
    val listStates = state.boards.mapIndexed { index, board ->
        key(index) { rememberLazyListState(initialFirstVisibleItemIndex = board.openAt) }
    }
    if (state.error != null) {
        val banner = startBannerOf(state.error)
        AttentionBanner(
            icon = banner.icon,
            text = stringResource(banner.text, vehicleId),
            action = banner.action?.let { stringResource(it.label) },
            onAction = when (banner.action) {
                BannerAction.CHANGE_BUS -> onChangeBus
                BannerAction.TRY_AGAIN -> onTryAgain
                BannerAction.RELOAD -> onReload
                null -> ({})
            },
            modifier = Modifier.padding(start = 16.dp, end = 16.dp, bottom = 16.dp),
        )
    }
    if (state.boards.size > 1) {
        DirectionSwitch(state.boards, selected, onSelect = { savedBoard = it })
    }
    BoardHeader(state.page.serviceDate, state.boards[selected].direction)
    ScrolledUnderDivider({ listStates[selected].canScrollBackward })
    Crossfade(targetState = selected, animationSpec = AppMotion.fastEffectsSpec(), label = "direction") { index ->
        BoardList(state.boards[index], listStates[index], state.startingRunId, onRunClick)
    }
}

@Composable
private fun DirectionSwitch(boards: List<Board>, selected: Int, onSelect: (Int) -> Unit) {
    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
        boards.forEachIndexed { index, board ->
            SegmentedButton(
                selected = index == selected,
                onClick = { onSelect(index) },
                shape = SegmentedButtonDefaults.itemShape(index, boards.size),
                icon = {
                    SegmentedButtonDefaults.Icon(
                        active = index == selected,
                        activeContent = {
                            Icon(
                                painter = painterResource(R.drawable.ic_check),
                                contentDescription = null,
                                modifier = Modifier.size(SegmentedButtonDefaults.IconSize),
                            )
                        },
                    )
                },
            ) {
                Text(
                    text = stringResource(R.string.runs_direction, board.direction?.headsign.orEmpty()),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** The service day in full, and where the selected direction's runs go from and to. */
@Composable
private fun BoardHeader(serviceDate: String, direction: RunDirection?) {
    Column(
        modifier = Modifier.padding(start = 24.dp, top = 16.dp, end = 24.dp, bottom = 8.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Text(
            text = serviceDay(serviceDate),
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.semantics { heading() },
        )
        if (direction != null) {
            Text(
                text = stringResource(R.string.runs_row_stops, direction.firstStop, direction.lastStop),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** `"20261008"` as the driver's language writes a day, "Thursday, October 8"; as it came if it is not a date. */
@Composable
private fun serviceDay(serviceDate: String): String {
    val locale = LocalConfiguration.current.locales[0]
    return remember(serviceDate, locale) {
        runCatching {
            val day = LocalDate.parse(serviceDate, DateTimeFormatter.BASIC_ISO_DATE)
            day.format(DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "EEEEMMMMd"), locale))
        }.getOrDefault(serviceDateLabel(serviceDate))
    }
}

@Composable
private fun BoardList(board: Board, listState: LazyListState, startingRunId: String?, onRunClick: (String) -> Unit) {
    LazyColumn(state = listState, modifier = Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
        val split = board.nowAt ?: 0
        runs(board.runs.subList(0, split), startingRunId, onRunClick)
        if (board.nowAt != null) {
            item(key = "now", contentType = "line") { NowLine() }
        }
        runs(board.runs.subList(split, board.runs.size), startingRunId, onRunClick)
    }
}

private fun LazyListScope.runs(runs: List<BoardRun>, startingRunId: String?, onRunClick: (String) -> Unit) {
    items(runs, key = { it.id }, contentType = { "run" }) { run ->
        RunRow(run, starting = run.id == startingRunId, dimmed = startingRunId != null && run.id != startingRunId, onRunClick)
    }
}

/**
 * One departure: the start time big, where it goes under it, and the Now or Next tag. While a start is in flight the
 * run being started is marked and the rest dim and ignore taps.
 */
@Composable
private fun RunRow(run: BoardRun, starting: Boolean, dimmed: Boolean, onRunClick: (String) -> Unit) {
    val startingLabel = stringResource(R.string.runs_starting)
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (starting) Modifier.background(MaterialTheme.colorScheme.surfaceContainerHigh) else Modifier)
            .heightIn(min = 72.dp)
            .clickable(enabled = !starting && !dimmed, role = Role.Button) { onRunClick(run.id) }
            .padding(start = 24.dp, end = 20.dp)
            .then(
                if (starting) {
                    Modifier.semantics {
                        stateDescription = startingLabel
                        liveRegion = LiveRegionMode.Polite
                    }
                } else {
                    Modifier
                },
            ),
        horizontalArrangement = Arrangement.spacedBy(14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                text = run.time,
                style = MaterialTheme.typography.departureTime,
                color = if (run.past || dimmed) quiet else MaterialTheme.colorScheme.onSurface,
            )
            Text(text = run.headsign, style = MaterialTheme.typography.bodyMedium, color = quiet, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        when {
            // The row says it is starting; the spinner would only repeat it.
            starting -> CircularProgressIndicator(modifier = Modifier.size(24.dp).clearAndSetSemantics {}, strokeWidth = 3.dp)
            run.highlight != null -> HighlightTag(run.highlight)
        }
    }
}

@Composable
private fun HighlightTag(highlight: RunHighlight) {
    Box(
        modifier = Modifier
            .background(MaterialTheme.colorScheme.primaryContainer, TagShape)
            .heightIn(min = 28.dp)
            .padding(horizontal = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = stringResource(
                when (highlight) {
                    RunHighlight.NOW -> R.string.runs_badge_now
                    RunHighlight.NEXT -> R.string.runs_badge_next
                },
            ),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onPrimaryContainer,
        )
    }
}

/** A dot and a line between the runs that have gone and the rest, which TalkBack reads as "Now". */
@Composable
private fun NowLine() {
    val now = stringResource(R.string.runs_badge_now)
    val primary = MaterialTheme.colorScheme.primary
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(12.dp)
            .padding(start = 18.dp, end = 24.dp)
            .semantics { contentDescription = now },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(12.dp).background(primary, CircleShape))
        Box(Modifier.weight(1f).height(2.dp).background(primary))
    }
}

/** What the banner over the board offers once the server or the schedule refuses a start. */
enum class BannerAction(@StringRes val label: Int) {
    CHANGE_BUS(R.string.runs_change_bus),
    TRY_AGAIN(R.string.runs_retry_button),
    RELOAD(R.string.runs_reload_button),
}

/** A refused start as the banner shows it. [text] may name the bus, as its one argument. */
data class StartBanner(@DrawableRes val icon: Int, @StringRes val text: Int, val action: BannerAction?)

/**
 * Each refusal with the one action that fixes it, or none when there is nothing to do here: an active trip ends on
 * the phone that started it, and a run with nothing to follow is not fixed by trying it again.
 */
internal fun startBannerOf(error: TripError): StartBanner = when (error) {
    TripError.NOT_ASSIGNED -> StartBanner(R.drawable.ic_no_transfer, R.string.runs_error_not_assigned, BannerAction.CHANGE_BUS)
    TripError.TRIP_ACTIVE -> StartBanner(R.drawable.ic_info, R.string.runs_error_trip_active, null)
    TripError.PREVIOUS_TRIP_NOT_ENDED -> StartBanner(R.drawable.ic_sync_problem, R.string.runs_error_previous_trip_not_ended, BannerAction.TRY_AGAIN)
    TripError.NETWORK -> StartBanner(R.drawable.ic_cloud_off, R.string.runs_error_network, BannerAction.TRY_AGAIN)
    TripError.NO_GEOMETRY -> StartBanner(R.drawable.ic_wrong_location, R.string.runs_error_no_geometry, null)
    TripError.TRIP_NOT_ACTIVE -> StartBanner(R.drawable.ic_event_repeat, R.string.runs_error_trip_not_active, BannerAction.RELOAD)
    TripError.OTHER -> StartBanner(R.drawable.ic_error, R.string.runs_error_other, BannerAction.TRY_AGAIN)
}
