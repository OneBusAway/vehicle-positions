package org.onebusaway.vehicletracker.ui.tracking

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.calculateStartPadding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.BottomSheetDefaults
import androidx.compose.material3.BottomSheetScaffold
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SheetValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberBottomSheetScaffoldState
import androidx.compose.material3.rememberStandardBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleStartEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.ui.map.RouteMap
import org.onebusaway.vehicletracker.ui.map.glEsVersion
import org.onebusaway.vehicletracker.ui.map.supportsMap
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** The gap between the screen's edges and the status card floating over the map. */
private val CardMargin = 12.dp

/** How far the panel is pulled up before the rows under the trip bar have fully come in: about one row. */
private val RowsFadeDistance = 48.dp

@Composable
fun TrackingScreen(
    onTripEnded: () -> Unit,
    onReauthRequired: () -> Unit,
    viewModel: TrackingViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    LifecycleStartEffect(viewModel) {
        viewModel.onScreenVisible(true)
        onStopOrDispose { viewModel.onScreenVisible(false) }
    }
    val context = LocalContext.current
    val mapSupported = remember { supportsMap(context.glEsVersion()) }
    TrackingScreenContent(
        state = state,
        onEndTripClick = { viewModel.onEndTrip(onTripEnded) },
        onEndTripLocallyClick = { viewModel.onEndTripLocally(onTripEnded) },
        onDismissError = viewModel::dismissEndTripError,
        onReauthClick = onReauthRequired,
        map = { modifier, contentPadding, controlsPadding ->
            // With no geometry there is no route to draw, and the status card already says so.
            val geometry = state.tracking.geometry
            if (mapSupported && geometry != null) {
                val ring = AppTheme.statusColors[vehicleTone(state.tracking.adherence)].text
                RouteMap(geometry, state.tracking.adherence, state.mapFile, ring.toArgb(), contentPadding, controlsPadding, modifier)
            } else {
                Spacer(modifier)
            }
        },
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrackingScreenContent(
    state: TrackingUiState,
    onEndTripClick: () -> Unit,
    onEndTripLocallyClick: () -> Unit,
    onDismissError: () -> Unit,
    onReauthClick: () -> Unit,
    // The map is handed in so that this screen can be shown, in tests and previews, with no
    // OpenGL behind it. It fills the screen: its content padding says how much of it the status
    // card and the trip panel cover, and its controls padding where its own buttons go, above
    // the trip bar.
    map: @Composable (modifier: Modifier, contentPadding: PaddingValues, controlsPadding: PaddingValues) -> Unit =
        { modifier, _, _ -> Spacer(modifier) },
) {
    var showConfirmDialog by remember { mutableStateOf(false) }
    val density = LocalDensity.current
    var cardHeight by remember { mutableIntStateOf(0) }
    var statusAreaHeight by remember { mutableIntStateOf(0) }
    // Material's drag handle and a bar of one line of text: what the panel shows before they are measured.
    var handleHeight by remember { mutableIntStateOf(with(density) { 48.dp.roundToPx() }) }
    var barHeight by remember { mutableIntStateOf(with(density) { 68.dp.roundToPx() }) }
    var panelHeight by remember { mutableIntStateOf(0) }
    val sheetState = rememberStandardBottomSheetState(skipHiddenState = true)
    val scope = rememberCoroutineScope()

    val statusInsets = WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal)
    val cardBottom = with(density) { (statusInsets.getTop(density) + cardHeight).toDp() } + CardMargin
    val navigationBarPx = WindowInsets.navigationBars.getBottom(density)
    val peekHeight = with(density) { (handleHeight + barHeight + navigationBarPx).toDp() }
    // Read where the panel settles, never while it is dragged: MapLibre jitters if its padding
    // changes on every frame.
    val expanded = sheetState.currentValue == SheetValue.Expanded
    val mapPadding = PaddingValues(
        top = cardBottom,
        bottom = if (expanded) with(density) { (handleHeight + panelHeight).toDp() } else peekHeight,
    )
    val controlsPadding = WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal).asPaddingValues()
        .let { PaddingValues(start = it.calculateStartPadding(LocalLayoutDirection.current), bottom = peekHeight) }
    val model = statusCardModel(state.tracking, state.activeTrip?.vehicleId)

    BackHandler(enabled = expanded) { scope.launch { sheetState.partialExpand() } }

    // Unlike the other screens, Tracking draws under the system bars: the map fills the whole
    // screen, the status card floats over its top, clear of the status bar, and the trip bar
    // pulls up from its bottom into the panel.
    BoxWithConstraints(Modifier.fillMaxSize()) {
        // The panel stops below the status area when pulled all the way up, where it would
        // otherwise slide under the card, or under the problem line or Signed out card below it.
        // The map keeps its padding from the card alone, so it never moves when a problem comes.
        val panelMaxHeight = maxHeight - with(density) { (statusAreaHeight + handleHeight).toDp() }
        val collapsedTop = constraints.maxHeight - with(density) { peekHeight.toPx() }
        val rowsFade = with(density) { RowsFadeDistance.toPx() }
        BottomSheetScaffold(
            scaffoldState = rememberBottomSheetScaffoldState(bottomSheetState = sheetState),
            sheetPeekHeight = peekHeight,
            sheetDragHandle = {
                BottomSheetDefaults.DragHandle(
                    modifier = Modifier.onSizeChanged { handleHeight = it.height },
                    width = 40.dp,
                    height = 4.dp,
                    color = MaterialTheme.colorScheme.outline,
                )
            },
            sheetContent = {
                TripPanel(
                    state = state,
                    model = model,
                    onEndTripClick = { showConfirmDialog = true },
                    onBarClick = { scope.launch { sheetState.expand() } },
                    onBarHeightChanged = { barHeight = it },
                    // Before the panel's first placement there is no offset to read.
                    rowsAlpha = { panelRowsAlpha(collapsedTop, runCatching { sheetState.requireOffset() }.getOrNull(), rowsFade) },
                    navigationBarPx = navigationBarPx,
                    modifier = Modifier.heightIn(max = panelMaxHeight).onSizeChanged { panelHeight = it.height },
                )
            },
        ) {
            Box(Modifier.fillMaxSize()) {
                map(Modifier.fillMaxSize(), mapPadding, controlsPadding)
                StatusArea(
                    model = model,
                    problem = state.tracking.problem,
                    onSignIn = onReauthClick,
                    // Measured outside its insets and margins, so the panel's limit counts them too.
                    modifier = Modifier
                        .fillMaxWidth()
                        .onSizeChanged { statusAreaHeight = it.height }
                        .windowInsetsPadding(statusInsets)
                        .padding(CardMargin),
                    onCardHeightChanged = { cardHeight = it },
                )
            }
        }
    }

    if (showConfirmDialog) {
        AlertDialog(
            onDismissRequest = { showConfirmDialog = false },
            icon = { Icon(painterResource(R.drawable.ic_stop_circle), contentDescription = null) },
            iconContentColor = MaterialTheme.colorScheme.onSurfaceVariant,
            title = { Text(stringResource(R.string.tracking_end_trip_dialog_title)) },
            text = { Text(endTripMessage(state.tracking)) },
            confirmButton = {
                InkButton(stringResource(R.string.tracking_end_trip_dialog_confirm)) {
                    showConfirmDialog = false
                    onEndTripClick()
                }
            },
            dismissButton = {
                QuietButton(stringResource(R.string.tracking_end_trip_dialog_cancel)) { showConfirmDialog = false }
            },
        )
    }

    if (state.endTripError) {
        EndTripErrorDialog(onRetry = onEndTripClick, onEndLocally = onEndTripLocallyClick, onDismiss = onDismissError)
    }
}

/**
 * How long the trip has run, ticking once a second. The tick lives here rather than in the screen, so each
 * second recomposes this text alone and not the status card or the map.
 */
@Composable
internal fun TripDuration(startedAtEpochSec: Long?, style: TextStyle, color: Color) {
    var elapsedSeconds by remember { mutableLongStateOf(0L) }
    LaunchedEffect(startedAtEpochSec) {
        if (startedAtEpochSec != null) {
            while (true) {
                elapsedSeconds = (System.currentTimeMillis() / 1000) - startedAtEpochSec
                delay(1000)
            }
        }
    }
    Text(text = formatDuration(elapsedSeconds), style = style, color = color)
}

/** What ending the trip will do, with the run's name when the phone has it, and how many reports went out. */
@Composable
private fun endTripMessage(tracking: TrackingState): String {
    val geometry = tracking.geometry
    val firstStop = geometry?.stops?.firstOrNull()
    val sent = tracking.fixesSent
    return if (geometry != null && firstStop != null && geometry.routeShortName.isNotEmpty()) {
        val startsAt = formatClock(firstStop.departureAt, geometry.timezone)
        pluralStringResource(R.plurals.tracking_end_trip_dialog_message, sent, geometry.routeShortName, startsAt, sent)
    } else {
        pluralStringResource(R.plurals.tracking_end_trip_dialog_message_no_run, sent, sent)
    }
}

/** The server did not confirm the end. Shared with the resume prompt, which ends trips through [TripEnder] too. */
@Composable
internal fun EndTripErrorDialog(onRetry: () -> Unit, onEndLocally: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(painterResource(R.drawable.ic_cloud_off), contentDescription = null) },
        iconContentColor = AppTheme.problemColors.iconOnSurface,
        title = { Text(stringResource(R.string.tracking_end_trip_error_title)) },
        text = { Text(stringResource(R.string.tracking_end_trip_error_message)) },
        confirmButton = { InkButton(stringResource(R.string.tracking_end_trip_error_retry), onRetry) },
        dismissButton = { QuietButton(stringResource(R.string.tracking_end_trip_error_end_locally), onEndLocally) },
    )
}

/** A dialog's main action, filled in ink rather than red: red means early on this screen. */
@Composable
private fun InkButton(text: String, onClick: () -> Unit) {
    Button(
        onClick = onClick,
        colors = ButtonDefaults.buttonColors(
            containerColor = MaterialTheme.colorScheme.onSurface,
            contentColor = MaterialTheme.colorScheme.surface,
        ),
    ) {
        Text(text)
    }
}

/** A dialog's other action, in ink rather than the theme's green. */
@Composable
private fun QuietButton(text: String, onClick: () -> Unit) {
    TextButton(onClick = onClick, colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.onSurface)) {
        Text(text)
    }
}
