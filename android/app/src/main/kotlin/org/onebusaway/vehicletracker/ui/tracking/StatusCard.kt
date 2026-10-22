package org.onebusaway.vehicletracker.ui.tracking

import androidx.annotation.DrawableRes
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.text
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.statusHero
import org.onebusaway.vehicletracker.ui.theme.statusLine
import org.onebusaway.vehicletracker.ui.theme.tripTitle
import java.time.ZoneId

// The status card and the problem line under it read as one piece: their facing corners are tighter.
private val CardShape = RoundedCornerShape(24.dp)
private val JoinedCardShape = RoundedCornerShape(topStart = 24.dp, topEnd = 24.dp, bottomStart = 8.dp, bottomEnd = 8.dp)
private val ProblemLineShape = RoundedCornerShape(topStart = 8.dp, topEnd = 8.dp, bottomStart = 24.dp, bottomEnd = 24.dp)
private val DiscSize = 52.dp
private const val DISC_ALPHA = 0.16f
private val Elevation = 1.dp

/**
 * The top of the Tracking screen: the status card, with a problem line joined under it while reports are not
 * getting through, or the Signed out card in their place once they have stopped. Every line is one line, so the
 * card is the same height in every state and nothing under it moves when the status changes.
 *
 * [onCardHeightChanged] hands back the status card's own height, without the problem line, for the map to keep
 * the vehicle clear of: a line that comes and goes with the signal should not move the map each time.
 */
@Composable
internal fun StatusArea(
    model: StatusCardModel,
    problem: TrackingProblem,
    onSignIn: () -> Unit,
    modifier: Modifier = Modifier,
    onCardHeightChanged: (Int) -> Unit = {},
) {
    // Unclipped, so the cards' shadows show over the map.
    AnimatedContent(
        targetState = problem == TrackingProblem.AUTH_EXPIRED,
        modifier = modifier,
        transitionSpec = {
            (fadeIn(AppMotion.defaultEffectsSpec()) togetherWith fadeOut(AppMotion.defaultEffectsSpec()))
                .using(SizeTransform(clip = false))
        },
        label = "signed out",
    ) { signedOut ->
        if (signedOut) {
            SignedOutCard(onSignIn)
        } else {
            val line = problemLine(problem)
            Column {
                StatusCard(model, joined = line != null, Modifier.onSizeChanged { onCardHeightChanged(it.height) })
                AnimatedContent(
                    targetState = line,
                    transitionSpec = {
                        (fadeIn(AppMotion.defaultEffectsSpec()) togetherWith fadeOut(AppMotion.defaultEffectsSpec()))
                            .using(SizeTransform(clip = false) { _, _ -> AppMotion.defaultSpatialSpec() })
                    },
                    label = "problem line",
                ) { shown ->
                    if (shown != null) ProblemLine(shown, Modifier.padding(top = 3.dp))
                }
            }
        }
    }
}

@Composable
private fun StatusCard(model: StatusCardModel, joined: Boolean, modifier: Modifier = Modifier) {
    val colors = AppTheme.statusColors[model.tone]
    // Drawn rather than set as a background, so a change of tone animates in the draw phase alone.
    val fill by animateColorAsState(colors.fill, AppMotion.defaultEffectsSpec(), label = "status fill")
    val shape = if (joined) JoinedCardShape else CardShape
    CompositionLocalProvider(LocalContentColor provides colors.onFill) {
        Row(
            modifier = modifier
                .fillMaxWidth()
                .shadow(Elevation, shape)
                .clip(shape)
                .drawBehind { drawRect(fill) }
                .padding(start = 16.dp, top = 16.dp, end = 20.dp, bottom = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Box(
                modifier = Modifier
                    .padding(top = 2.dp)
                    .size(DiscSize)
                    .background(colors.onFill.copy(alpha = DISC_ALPHA), CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Icon(painterResource(statusIcon(model)), contentDescription = null, modifier = Modifier.size(30.dp))
            }
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                val announcement = statusAnnouncement(model)
                val heroStyle = MaterialTheme.typography.statusHero
                Text(
                    text = statusAnswer(model),
                    style = heroStyle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    autoSize = TextAutoSize.StepBased(minFontSize = 28.sp, maxFontSize = 40.sp, stepSize = 2.sp),
                    modifier = Modifier
                        // A smaller size has a smaller line too, so the answer keeps the full line's height
                        // and the card never changes height with it.
                        .height(with(LocalDensity.current) { heroStyle.lineHeight.toDp() })
                        .wrapContentHeight()
                        // TalkBack announces the answer once each time it changes, and never the distances,
                        // which change with every fix.
                        .clearAndSetSemantics {
                            text = AnnotatedString(announcement)
                            liveRegion = LiveRegionMode.Polite
                        },
                )
                StatusLines(model)
            }
        }
    }
}

@DrawableRes
private fun statusIcon(model: StatusCardModel): Int = when (model) {
    is StatusCardModel.Adherence -> when (model.status) {
        AdherenceStatus.LATE -> R.drawable.ic_hourglass_bottom
        AdherenceStatus.EARLY -> R.drawable.ic_speed
        else -> R.drawable.ic_check_circle
    }
    is StatusCardModel.OffSchedule, StatusCardModel.NoSchedule -> R.drawable.ic_event_busy
    is StatusCardModel.OffRoute -> R.drawable.ic_wrong_location
    is StatusCardModel.WaitingForGps -> R.drawable.ic_location_searching
}

/** The one answer, in the card's largest type. */
@Composable
private fun statusAnswer(model: StatusCardModel): String = when (model) {
    is StatusCardModel.Adherence -> deviationText(model.deviationSec)
    is StatusCardModel.OffSchedule -> deviationText(model.deviationSec)
    is StatusCardModel.OffRoute -> stringResource(R.string.tracking_adherence_off_route, distanceText(model.distanceFromRouteM))
    is StatusCardModel.WaitingForGps -> stringResource(R.string.tracking_adherence_waiting_for_gps)
    StatusCardModel.NoSchedule -> stringResource(R.string.tracking_adherence_no_schedule)
}

/** The answer as TalkBack announces it: off the route, without the distance. */
@Composable
private fun statusAnnouncement(model: StatusCardModel): String =
    if (model is StatusCardModel.OffRoute) stringResource(R.string.tracking_adherence_off_route_short) else statusAnswer(model)

/** The card's two lines under the answer: the stop it was judged against, then that stop's time and distance. */
@Composable
private fun StatusLines(model: StatusCardModel) {
    val line1: String
    val line1Emphasis: String?
    val line2: String
    when (model) {
        is StatusCardModel.Adherence -> {
            line1 = stringResource(R.string.tracking_adherence_next_stop)
            line1Emphasis = model.nextStop.name
            line2 = nextStopDetail(model.nextStop, model.distanceToNextStopM, model.zone)
        }
        is StatusCardModel.OffSchedule -> {
            line1 = stringResource(R.string.tracking_adherence_off_schedule)
            line1Emphasis = stringResource(R.string.tracking_adherence_right_run)
            line2 = stringResource(
                R.string.tracking_adherence_off_schedule_next,
                model.nextStop.name,
                formatClock(model.nextStop.arrivalAt, model.zone),
            )
        }
        is StatusCardModel.OffRoute -> {
            line1 = stringResource(R.string.tracking_adherence_next_stop)
            line1Emphasis = model.nextStop.name
            line2 = nextStopDetail(model.nextStop, model.distanceToNextStopM, model.zone)
        }
        is StatusCardModel.WaitingForGps -> {
            val firstStop = model.firstStop
            val zone = model.zone
            if (firstStop != null && zone != null) {
                line1 = stringResource(R.string.tracking_adherence_from)
                line1Emphasis = firstStop.name
                val startsAt = formatClock(firstStop.departureAt, zone)
                line2 = model.vehicleId?.let { stringResource(R.string.tracking_adherence_run_vehicle, startsAt, it) }
                    ?: stringResource(R.string.tracking_adherence_run, startsAt)
            } else {
                // The trip's stops have not loaded yet. Empty lines keep the card its usual height.
                line1 = ""
                line1Emphasis = null
                line2 = ""
            }
        }
        StatusCardModel.NoSchedule -> {
            line1 = stringResource(R.string.tracking_adherence_no_schedule_riders)
            line1Emphasis = null
            line2 = stringResource(R.string.tracking_adherence_no_schedule_detail)
        }
    }
    Row(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(line1, style = MaterialTheme.typography.statusLine, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (line1Emphasis != null) {
            Text(
                text = line1Emphasis,
                style = MaterialTheme.typography.tripTitle,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
        }
    }
    Text(line2, style = MaterialTheme.typography.statusLine, maxLines = 1, overflow = TextOverflow.Ellipsis)
}

/** The stop's scheduled time on the agency's clock, then how far along the route it is. */
@Composable
private fun nextStopDetail(stop: TripStop, distanceM: Double, zone: ZoneId): String =
    stringResource(R.string.tracking_adherence_next_stop_detail, formatClock(stop.arrivalAt, zone), distanceText(distanceM))

/** What the problem line says: what is wrong, then what it means for the driver. */
private data class ProblemLineText(val icon: Int, val title: Int, val detail: Int)

private fun problemLine(problem: TrackingProblem): ProblemLineText? = when (problem) {
    TrackingProblem.NO_NETWORK ->
        ProblemLineText(R.drawable.ic_cloud_off, R.string.tracking_problem_no_network, R.string.tracking_problem_no_network_detail)
    TrackingProblem.NO_GPS ->
        ProblemLineText(R.drawable.ic_location_disabled, R.string.tracking_problem_no_gps, R.string.tracking_problem_no_gps_detail)
    TrackingProblem.CLOCK_SKEW ->
        ProblemLineText(R.drawable.ic_schedule, R.string.tracking_problem_clock_skew, R.string.tracking_problem_clock_skew_detail)
    TrackingProblem.REPORTS_REFUSED ->
        ProblemLineText(R.drawable.ic_no_transfer, R.string.tracking_problem_reports_refused, R.string.tracking_problem_reports_refused_detail)
    // No line when all is well, and none for an expired sign-in: the Signed out card takes the status card's place.
    TrackingProblem.NONE, TrackingProblem.AUTH_EXPIRED -> null
}

@Composable
private fun ProblemLine(line: ProblemLineText, modifier: Modifier = Modifier) {
    val colors = AppTheme.problemColors
    Row(
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = 52.dp)
            .shadow(Elevation, ProblemLineShape)
            .background(colors.container, ProblemLineShape)
            .padding(horizontal = 18.dp)
            .semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite },
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(painterResource(line.icon), contentDescription = null, tint = colors.icon, modifier = Modifier.size(24.dp))
        Text(
            text = stringResource(line.title),
            color = colors.content,
            style = MaterialTheme.typography.titleMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = stringResource(R.string.tracking_separator),
            color = colors.content,
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.clearAndSetSemantics {},
        )
        Text(
            text = stringResource(line.detail),
            color = colors.content,
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
    }
}

/** In the status card's place once the sign-in has expired: reporting has stopped until the driver signs in. */
@Composable
private fun SignedOutCard(onSignIn: () -> Unit) {
    val colors = AppTheme.problemColors.signedOut
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .shadow(Elevation, CardShape)
            .background(colors.container, CardShape)
            .padding(18.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            Box(
                modifier = Modifier.size(DiscSize).background(colors.accent, CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Icon(painterResource(R.drawable.ic_lock), contentDescription = null, tint = colors.onAccent, modifier = Modifier.size(28.dp))
            }
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(
                    text = stringResource(R.string.tracking_signed_out_title),
                    color = colors.content,
                    style = MaterialTheme.typography.headlineLarge,
                    modifier = Modifier.semantics { heading() },
                )
                Text(
                    text = stringResource(R.string.tracking_signed_out_message),
                    color = colors.content,
                    style = MaterialTheme.typography.statusLine,
                )
            }
        }
        Button(
            onClick = onSignIn,
            colors = ButtonDefaults.buttonColors(containerColor = colors.accent, contentColor = colors.onAccent),
            modifier = Modifier.fillMaxWidth().heightIn(min = 56.dp),
        ) {
            Icon(painterResource(R.drawable.ic_login), contentDescription = null, modifier = Modifier.size(22.dp))
            Spacer(Modifier.width(8.dp))
            Text(stringResource(R.string.tracking_reauth_button), style = MaterialTheme.typography.titleMedium)
        }
    }
}
