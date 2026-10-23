package org.onebusaway.vehicletracker.ui.resume

import androidx.activity.compose.BackHandler
import androidx.annotation.DrawableRes
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.delay
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.AttentionBanner
import org.onebusaway.vehicletracker.ui.components.SPINNER_DELAY_MS
import org.onebusaway.vehicletracker.ui.routes.RouteBadge
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.tripTitle
import org.onebusaway.vehicletracker.ui.tracking.formatClock
import java.time.Instant
import java.time.ZoneId

private val CardShape = RoundedCornerShape(24.dp)
private val BadgeShape = RoundedCornerShape(14.dp)
private const val SECONDS_PER_MINUTE = 60L

@Composable
fun ResumeShiftScreen(
    onResumed: () -> Unit,
    onEnded: () -> Unit,
    viewModel: ResumeShiftViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    ResumeShiftScreenContent(
        state = state,
        now = viewModel::nowEpochSec,
        onResumeClick = { viewModel.onResumeShift(onResumed) },
        onEndShiftClick = { viewModel.onEndShift(onEnded) },
        onEndShiftLocallyClick = { viewModel.onEndShiftLocally(onEnded) },
        onDismissEndError = viewModel::dismissEndShiftError,
    )
}

/**
 * The trip that was running when the app closed, as the driver knows it, and the two ways on: Resume, the usual one,
 * and End trip. An end the server did not confirm is said here, above the buttons, which then offer to try again or
 * to end it on this phone only; a third button, or Back, goes back to Resume for a driver who did not mean to end it.
 * [now] is the time in epoch seconds, read once a minute.
 */
@Composable
fun ResumeShiftScreenContent(
    state: ResumeShiftUiState,
    now: () -> Long,
    onResumeClick: () -> Unit,
    onEndShiftClick: () -> Unit,
    onEndShiftLocallyClick: () -> Unit,
    onDismissEndError: () -> Unit,
) {
    // Until the stored trip loads there is nothing to resume, and End trip has nothing to end.
    val actionsEnabled = state.trip != null && !state.ending
    BackHandler(enabled = state.endTripError, onBack = onDismissEndError)
    BoxWithConstraints(Modifier.fillMaxSize()) {
        // One scroll for the page: the buttons sit at the bottom, and a large font scale scrolls them into reach.
        Column(
            modifier = Modifier
                .verticalScroll(rememberScrollState())
                .heightIn(min = maxHeight)
                .padding(start = 24.dp, top = 56.dp, end = 24.dp, bottom = 24.dp),
            verticalArrangement = Arrangement.SpaceBetween,
        ) {
            Column {
                Text(
                    text = stringResource(R.string.resume_title),
                    style = MaterialTheme.typography.displaySmall,
                    modifier = Modifier.semantics { heading() },
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    text = stringResource(R.string.resume_message),
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(28.dp))
                if (state.card != null) TripCard(state.card, now) else LoadingCard()
                AnimatedVisibility(
                    visible = state.endTripError,
                    enter = expandVertically(AppMotion.defaultSpatialSpec()) + fadeIn(AppMotion.defaultEffectsSpec()),
                    exit = shrinkVertically(AppMotion.defaultSpatialSpec()) + fadeOut(AppMotion.defaultEffectsSpec()),
                ) {
                    AttentionBanner(
                        icon = R.drawable.ic_cloud_off,
                        text = stringResource(R.string.resume_end_failed),
                        modifier = Modifier.padding(top = 20.dp),
                    )
                }
            }
            Column(Modifier.padding(top = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                // Once an end failed, the trip is ending either way: the two buttons say how.
                Button(
                    onClick = if (state.endTripError) onEndShiftClick else onResumeClick,
                    enabled = actionsEnabled,
                    modifier = Modifier.fillMaxWidth().height(56.dp),
                ) {
                    Text(
                        text = stringResource(if (state.endTripError) R.string.tracking_end_trip_error_retry else R.string.resume_button),
                        style = MaterialTheme.typography.titleMedium,
                    )
                }
                OutlinedButton(
                    onClick = if (state.endTripError) onEndShiftLocallyClick else onEndShiftClick,
                    enabled = actionsEnabled,
                    colors = ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.onSurface),
                    modifier = Modifier.fillMaxWidth().height(56.dp),
                ) {
                    if (state.ending) {
                        CircularProgressIndicator(
                            color = LocalContentColor.current,
                            strokeWidth = 2.dp,
                            modifier = Modifier.size(20.dp).clearAndSetSemantics {},
                        )
                        Spacer(Modifier.width(10.dp))
                    }
                    Text(
                        text = stringResource(
                            when {
                                state.ending -> R.string.resume_ending
                                state.endTripError -> R.string.tracking_end_trip_error_end_locally
                                else -> R.string.resume_end_trip_button
                            },
                        ),
                        style = MaterialTheme.typography.titleMedium,
                    )
                }
                if (state.endTripError) {
                    TextButton(
                        onClick = onDismissEndError,
                        enabled = actionsEnabled,
                        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.onSurface),
                        modifier = Modifier.fillMaxWidth().height(48.dp),
                    ) {
                        Text(stringResource(R.string.resume_end_failed_back), style = MaterialTheme.typography.titleMedium)
                    }
                }
            }
        }
    }
}

/** The route's badge, where the run goes and when it started, the bus, and when the trip began. */
@Composable
private fun TripCard(card: ResumeCard, now: () -> Long) {
    Column(
        modifier = Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer, CardShape).padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(18.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically) {
            RouteBadge(
                route = card.route,
                modifier = Modifier.sizeIn(minWidth = 56.dp, minHeight = 56.dp),
                style = MaterialTheme.typography.headlineMedium,
                shape = BadgeShape,
            )
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    text = card.headsign ?: stringResource(R.string.resume_route, card.route.shortName),
                    style = MaterialTheme.typography.tripTitle,
                )
                card.runStartsAt?.let { startsAt ->
                    Text(
                        text = stringResource(R.string.resume_run, formatClock(startsAt, card.zone)),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Fact(R.drawable.ic_directions_bus) { Text(stringResource(R.string.resume_bus, card.vehicleId), style = MaterialTheme.typography.bodyLarge) }
        Fact(R.drawable.ic_schedule_outline) { StartedAgo(card.startedAt, card.zone, now) }
    }
}

@Composable
private fun Fact(@DrawableRes icon: Int, content: @Composable () -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(painterResource(icon), contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(22.dp))
        content()
    }
}

/**
 * "Started 23 min ago, at 6:58 AM", in whole minutes: it reads the time again as each minute of the trip turns over,
 * so this text is all that changes while the prompt waits.
 */
@Composable
private fun StartedAgo(startedAt: Instant, zone: ZoneId, now: () -> Long) {
    val start = startedAt.epochSecond
    val elapsed by produceState((now() - start).coerceAtLeast(0), start) {
        while (true) {
            delay((SECONDS_PER_MINUTE - value % SECONDS_PER_MINUTE) * 1000)
            value = (now() - start).coerceAtLeast(0)
        }
    }
    val at = formatClock(startedAt, zone)
    val minutes = (elapsed / SECONDS_PER_MINUTE).toInt()
    Text(
        text = if (minutes < 60) {
            pluralStringResource(R.plurals.resume_started_minutes_ago, minutes, minutes, at)
        } else {
            stringResource(R.string.resume_started_hours_ago, minutes / 60, minutes % 60, at)
        },
        style = MaterialTheme.typography.bodyLarge,
    )
}

/** Shown only once reading the trip takes a moment, so a quick read goes straight to the card. */
@Composable
private fun LoadingCard() {
    var shown by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        delay(SPINNER_DELAY_MS)
        shown = true
    }
    Box(
        modifier = Modifier.fillMaxWidth().height(236.dp).background(MaterialTheme.colorScheme.surfaceContainer, CardShape),
        contentAlignment = Alignment.Center,
    ) {
        if (shown) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
                CircularProgressIndicator()
                Text(
                    text = stringResource(R.string.resume_loading),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
