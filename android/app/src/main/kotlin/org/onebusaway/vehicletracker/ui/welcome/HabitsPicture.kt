package org.onebusaway.vehicletracker.ui.welcome

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.theme.AppTheme

// Three rows joined into one block: round outside, nearly square where they meet.
private val FirstRowShape = RoundedCornerShape(topStart = 24.dp, topEnd = 24.dp, bottomStart = 8.dp, bottomEnd = 8.dp)
private val MiddleRowShape = RoundedCornerShape(8.dp)
private val LastRowShape = RoundedCornerShape(topStart = 8.dp, topEnd = 8.dp, bottomStart = 24.dp, bottomEnd = 24.dp)
private val PausedShape = RoundedCornerShape(24.dp)

/** The one small move a row's icon makes as it arrives. */
private enum class IconMove {
    /** The pin drops in from above and settles. */
    DROPS,

    /** The bolt flashes: up from small, past its size, and back. */
    FLASHES,

    /** The app turns in from a tilt and settles. */
    TURNS,
}

/**
 * Screen 3's picture: the three things a trip depends on, then what the Reports paused notification looks like.
 * It gives no permission and has no switch; the real asks stay on Before you start. Like the other pictures, every
 * piece sits in its place from the first frame and [clock] moves its layer.
 */
@Composable
internal fun HabitsPicture(clock: PictureClock, compact: Boolean, modifier: Modifier = Modifier) {
    Column(modifier) {
        Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
            HabitRow(
                0, clock, compact, FirstRowShape, R.drawable.ic_location_on, IconMove.DROPS,
                R.string.welcome_habits_location, R.string.welcome_habits_location_why,
            )
            HabitRow(
                1, clock, compact, MiddleRowShape, R.drawable.ic_bolt, IconMove.FLASHES,
                R.string.welcome_habits_charge, R.string.welcome_habits_charge_why,
            )
            HabitRow(
                2, clock, compact, LastRowShape, R.drawable.ic_open_in_phone, IconMove.TURNS,
                R.string.welcome_habits_open, R.string.welcome_habits_open_why,
            )
        }
        Text(
            text = stringResource(R.string.welcome_habits_if_stops),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier
                .graphicsLayer { alpha = WelcomeMotion.SlowFade.settle(clock.millis - HabitsTimeline.LABEL_SHOWS_AT) }
                .padding(start = 4.dp, top = if (compact) 10.dp else 22.dp, end = 4.dp, bottom = if (compact) 6.dp else 10.dp)
                .semantics { heading() },
        )
        ReportsPaused(clock, compact)
    }
}

@Composable
private fun HabitRow(
    index: Int,
    clock: PictureClock,
    compact: Boolean,
    shape: Shape,
    @DrawableRes icon: Int,
    move: IconMove,
    @StringRes title: Int,
    @StringRes why: Int,
) {
    val colors = MaterialTheme.colorScheme
    val movesAt = HabitsTimeline.iconMovesAt(index)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .risesIn(clock, HabitsTimeline.rowRisesAt(index))
            .background(colors.surfaceContainer, shape)
            .heightIn(min = if (compact) 56.dp else 76.dp)
            .padding(horizontal = if (compact) 14.dp else 16.dp, vertical = if (compact) 8.dp else 12.dp)
            // One element for TalkBack, the title then its reason.
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(if (compact) 12.dp else 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier.size(if (compact) 36.dp else 44.dp).background(colors.primaryContainer, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                painter = painterResource(icon),
                contentDescription = null,
                tint = colors.onPrimaryContainer,
                modifier = Modifier.size(24.dp).graphicsLayer {
                    val since = clock.millis - movesAt
                    val settled = WelcomeMotion.Pop.settle(since)
                    alpha = WelcomeMotion.Fade.settle(since)
                    when (move) {
                        IconMove.DROPS -> translationY = -(1f - settled) * 9.dp.toPx()
                        IconMove.FLASHES -> {
                            val grown = 0.7f + 0.3f * settled + 0.2f * WelcomeMotion.Pop.hop(since)
                            scaleX = grown
                            scaleY = grown
                        }
                        IconMove.TURNS -> {
                            val grown = 0.8f + 0.2f * settled
                            rotationZ = -(1f - settled) * 14f
                            scaleX = grown
                            scaleY = grown
                        }
                    }
                },
            )
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(stringResource(title), style = MaterialTheme.typography.titleMedium)
            Text(stringResource(why), style = MaterialTheme.typography.bodyMedium, color = colors.onSurfaceVariant)
        }
    }
}

/** The Reports paused notification as a dark strip, in its own words, so the driver knows it when it comes. */
@Composable
private fun ReportsPaused(clock: PictureClock, compact: Boolean) {
    val colors = AppTheme.problemColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .risesIn(clock, HabitsTimeline.CARD_RISES_AT)
            .background(colors.container, PausedShape)
            .heightIn(min = if (compact) 56.dp else 64.dp)
            .padding(horizontal = if (compact) 14.dp else 16.dp, vertical = if (compact) 8.dp else 12.dp)
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(if (compact) 12.dp else 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier.size(if (compact) 36.dp else 44.dp).background(colors.icon.copy(alpha = 0.16f), CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(painterResource(R.drawable.ic_notifications_active), contentDescription = null, tint = colors.icon, modifier = Modifier.size(24.dp))
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(
                text = stringResource(R.string.tracking_notification_resume_title),
                style = MaterialTheme.typography.titleMedium,
                color = colors.content,
            )
            Text(
                text = stringResource(R.string.tracking_notification_resume_text),
                style = MaterialTheme.typography.bodyMedium,
                color = colors.content,
            )
        }
    }
}
