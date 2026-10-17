package org.onebusaway.vehicletracker.ui.permissions

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.SetupBackButton
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.AppTheme

private val MarkerSize = 44.dp
private val AllowedShape = RoundedCornerShape(14.dp)

/** A row's icon, title and why, as the checklist shows them before anything is answered. */
private class RowText(@DrawableRes val icon: Int, @StringRes val title: Int, @StringRes val why: Int)

private fun textOf(row: PermissionRow): RowText = when (row) {
    PermissionRow.LOCATION -> RowText(R.drawable.ic_location_on, R.string.preflight_location, R.string.preflight_location_why)
    PermissionRow.PRECISE -> RowText(R.drawable.ic_my_location, R.string.preflight_precise, R.string.preflight_precise_why)
    PermissionRow.BACKGROUND -> RowText(R.drawable.ic_schedule_outline, R.string.preflight_background, R.string.preflight_background_why)
    PermissionRow.NOTIFICATIONS -> RowText(R.drawable.ic_notifications, R.string.preflight_notifications, R.string.preflight_notifications_why)
    PermissionRow.BATTERY -> RowText(R.drawable.ic_battery_saver, R.string.preflight_battery, R.string.preflight_battery_why)
    PermissionRow.LOCATION_SERVICES ->
        RowText(R.drawable.ic_location_searching, R.string.preflight_location_services, R.string.preflight_location_services_why)
}

/**
 * Everything the app needs to report [vehicleId], each with why, before Android asks for any of it. Continue walks
 * Android's prompts in turn while this stays on screen under them, and each answer changes only its own row. A row that
 * stops the trip offers the one thing that fixes it.
 */
@Composable
internal fun PreflightContent(
    vehicleId: String,
    rows: List<ChecklistRow>,
    canContinue: Boolean,
    onBack: () -> Unit,
    onContinue: () -> Unit,
    onRowAction: () -> Unit,
) {
    // Over the run board, which it hides until the run starts or the driver goes back.
    Surface(Modifier.fillMaxSize()) {
        Column {
            Row(Modifier.fillMaxWidth().height(56.dp).padding(start = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                SetupBackButton(onBack)
            }
            Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
                Text(
                    text = stringResource(R.string.preflight_title),
                    style = MaterialTheme.typography.headlineLarge,
                    modifier = Modifier.padding(start = 24.dp, top = 8.dp, end = 24.dp).semantics { heading() },
                )
                Text(
                    text = stringResource(R.string.preflight_message, vehicleId),
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(start = 24.dp, top = 8.dp, end = 24.dp, bottom = 20.dp),
                )
                rows.forEach { row ->
                    key(row.row) { PreflightRow(row, onRowAction) }
                }
            }
            Button(
                onClick = onContinue,
                enabled = canContinue,
                modifier = Modifier.fillMaxWidth().padding(24.dp).height(56.dp),
            ) {
                Text(stringResource(R.string.preflight_continue), style = MaterialTheme.typography.titleMedium)
            }
        }
    }
}

/** One thing the app needs. When its answer comes in, its marker and state change in place, with a quick fade. */
@Composable
private fun PreflightRow(row: ChecklistRow, onAction: () -> Unit) {
    val text = textOf(row.row)
    Row(
        modifier = Modifier.fillMaxWidth().heightIn(min = 76.dp).padding(horizontal = 24.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Row(
            modifier = Modifier.weight(1f).semantics(mergeDescendants = true) {},
            horizontalArrangement = Arrangement.spacedBy(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AnimatedContent(row.state.marker, transitionSpec = { crossFade() }, label = "marker") { marker ->
                Marker(marker, text.icon)
            }
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(stringResource(text.title), style = MaterialTheme.typography.titleMedium)
                Text(
                    text = stringResource(whyOf(row.state) ?: text.why),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        AnimatedContent(row.state, transitionSpec = { crossFade() }, label = "state") { state ->
            RowEnd(state, onAction)
        }
    }
}

private fun crossFade() = fadeIn(AppMotion.defaultEffectsSpec()) togetherWith fadeOut(AppMotion.defaultEffectsSpec())

/** What a row says in place of its why once answered, or null to keep it. */
@StringRes
private fun whyOf(state: RowState): Int? = when (state) {
    RowState.SKIPPED -> R.string.preflight_skipped_why
    RowState.NEEDED, RowState.NEEDED_IN_SETTINGS -> R.string.preflight_location_needed
    RowState.APPROXIMATE -> R.string.preflight_approximate
    else -> null
}

/** The state in words at the end of the row, which TalkBack reads out as it changes; or the action that unblocks it. */
@Composable
private fun RowEnd(state: RowState, onAction: () -> Unit) {
    val action = when (state) {
        RowState.NEEDED, RowState.APPROXIMATE, RowState.OFF -> R.string.preflight_try_again
        RowState.NEEDED_IN_SETTINGS -> R.string.preflight_open_settings
        else -> null
    }
    if (action != null) {
        TextButton(onClick = onAction) { Text(stringResource(action)) }
        return
    }
    val label = when (state) {
        RowState.ALLOWED -> R.string.preflight_allowed
        RowState.SKIPPED -> R.string.preflight_skipped
        RowState.NEXT -> R.string.preflight_next
        else -> return
    }
    Text(
        text = stringResource(label),
        style = MaterialTheme.typography.labelLarge,
        color = if (state == RowState.NEXT) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/** How a row's marker looks; the states that wait on the driver keep the look of the row Android is on. */
private enum class Marker { DONE, SKIPPED, CURRENT, PENDING }

private val RowState.marker: Marker
    get() = when (this) {
        RowState.ALLOWED -> Marker.DONE
        RowState.SKIPPED -> Marker.SKIPPED
        RowState.NOT_REACHED -> Marker.PENDING
        RowState.NEXT, RowState.NEEDED, RowState.NEEDED_IN_SETTINGS, RowState.APPROXIMATE, RowState.OFF -> Marker.CURRENT
    }

@Composable
private fun Marker(marker: Marker, @DrawableRes icon: Int) {
    val colors = MaterialTheme.colorScheme
    val look = when (marker) {
        Marker.DONE -> Modifier.background(colors.primary, AllowedShape)
        Marker.SKIPPED -> Modifier.border(2.dp, AppTheme.problemColors.iconOnSurface, CircleShape)
        Marker.CURRENT -> Modifier.background(colors.primaryContainer, CircleShape)
        Marker.PENDING -> Modifier.dashedRing(colors.outlineVariant)
    }
    val (glyph, tint) = when (marker) {
        Marker.DONE -> R.drawable.ic_check to colors.onPrimary
        Marker.SKIPPED -> R.drawable.ic_remove to AppTheme.problemColors.iconOnSurface
        Marker.CURRENT -> icon to colors.onPrimaryContainer
        Marker.PENDING -> icon to colors.onSurfaceVariant
    }
    Box(Modifier.size(MarkerSize).then(look), contentAlignment = Alignment.Center) {
        // The state is said in words at the end of the row.
        Icon(painterResource(glyph), contentDescription = null, tint = tint, modifier = Modifier.size(22.dp))
    }
}

private fun Modifier.dashedRing(color: Color) = drawBehind {
    val stroke = 2.dp.toPx()
    drawCircle(
        color = color,
        radius = (size.minDimension - stroke) / 2,
        center = Offset(size.width / 2, size.height / 2),
        style = Stroke(width = stroke, pathEffect = PathEffect.dashPathEffect(floatArrayOf(4.dp.toPx(), 4.dp.toPx()))),
    )
}
