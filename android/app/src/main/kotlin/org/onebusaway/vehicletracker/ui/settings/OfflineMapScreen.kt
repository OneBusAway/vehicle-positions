@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.settings

import android.text.format.Formatter
import androidx.annotation.DrawableRes
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLocale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.map.MapDetails
import org.onebusaway.vehicletracker.data.map.MapFailure
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.ui.components.GroupOuterCorner
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.InkButton
import org.onebusaway.vehicletracker.ui.components.QuietButton
import org.onebusaway.vehicletracker.ui.components.groupRow
import org.onebusaway.vehicletracker.ui.map.casingColor
import org.onebusaway.vehicletracker.ui.map.streetColors
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.time.ZoneId

private val CardShape = RoundedCornerShape(GroupOuterCorner)
private val PictureHeight = 110.dp

/** How much of the picture shows while there is no map to use. */
private const val FADED = 0.45f

/**
 * The agency's street map on this phone: where it stands, and getting it again or deleting it. [onGone] leaves once
 * there is none to show, as when the agency drops it; it is not a tap, so it is not dropped while the page comes in.
 */
@Composable
fun OfflineMapScreen(onBack: () -> Unit, onGone: () -> Unit, viewModel: OfflineMapViewModel = hiltViewModel()) {
    val view by viewModel.map.collectAsStateWithLifecycle()
    LaunchedEffect(view) { if (view == OfflineMapView.Read(null)) onGone() }
    // Its bar shows while the map is read, in a moment, and as the page leaves.
    OfflineMapContent((view as? OfflineMapView.Read)?.map, onBack, viewModel::onGetAgain, viewModel::onDelete, viewModel::onTryAgain)
}

@Composable
internal fun OfflineMapContent(
    map: OfflineMap?,
    onBack: () -> Unit,
    onGetAgain: () -> Unit,
    onDelete: () -> Unit,
    onTryAgain: () -> Unit,
    nowEpochSec: Long = System.currentTimeMillis() / 1000,
) {
    var confirming by rememberSaveable { mutableStateOf(false) }
    // Pinned: the bar stays put and takes the container colour once the page is under it.
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        TopAppBar(
            title = { Text(stringResource(R.string.offline_map_title), modifier = Modifier.semantics { heading() }) },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
                }
            },
            scrollBehavior = scrollBehavior,
        )
        if (map == null) return@Column
        Column(
            Modifier.verticalScroll(rememberScrollState()).padding(start = 16.dp, top = 8.dp, end = 16.dp, bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            StateCard(map)
            if (map is OfflineMap.Ready) Facts(map.details, nowEpochSec)
            Actions(map, onGetAgain, onAskDelete = { confirming = true }, onTryAgain)
            Text(
                stringResource(lineOf(map)),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
    }
    if (confirming) {
        AlertDialog(
            onDismissRequest = { confirming = false },
            title = { Text(stringResource(R.string.offline_map_delete_title)) },
            text = { Text(stringResource(R.string.offline_map_delete_message)) },
            confirmButton = {
                InkButton(stringResource(R.string.offline_map_delete_confirm), onClick = {
                    confirming = false
                    onDelete()
                })
            },
            dismissButton = { QuietButton(stringResource(R.string.offline_map_delete_cancel), onClick = { confirming = false }) },
        )
    }
}

/** The picture, faded unless the map is here, and under it where the map stands. */
@Composable
private fun StateCard(map: OfflineMap) {
    val colors = MaterialTheme.colorScheme
    val context = LocalContext.current
    val size = { bytes: Long -> Formatter.formatShortFileSize(context, bytes) }
    val title: String
    val detail: String?
    @DrawableRes val icon: Int
    var tint = colors.onSurfaceVariant
    when (map) {
        is OfflineMap.Ready -> {
            title = stringResource(R.string.tracking_street_map_ready)
            detail = stringResource(R.string.offline_map_ready_detail)
            icon = R.drawable.ic_check_circle
            tint = colors.primary
        }
        is OfflineMap.Downloading -> {
            title = stringResource(R.string.offline_map_downloading, map.percent)
            detail = if (map.total > 0) stringResource(R.string.offline_map_downloaded_of, size(map.bytes), size(map.total)) else null
            icon = R.drawable.ic_download
        }
        is OfflineMap.NoSpace -> {
            title = stringResource(R.string.tracking_street_map_no_space)
            detail = map.needs?.let { stringResource(R.string.offline_map_needs, size(it)) }
            icon = R.drawable.ic_sd_card_alert
            tint = AppTheme.problemColors.iconOnSurface
        }
        is OfflineMap.NotDownloaded -> {
            title = stringResource(R.string.tracking_street_map_not_downloaded)
            detail = stringResource(if (map.byItself) R.string.offline_map_tries_by_itself else R.string.offline_map_tries_when_opened)
            icon = R.drawable.ic_cloud_off
            tint = AppTheme.problemColors.iconOnSurface
        }
        is OfflineMap.Unusable -> {
            title = stringResource(if (map.needsHttps) R.string.offline_map_needs_https else R.string.offline_map_unusable)
            detail = stringResource(R.string.offline_map_tell_agency)
            icon = R.drawable.ic_error
            tint = AppTheme.problemColors.iconOnSurface
        }
        OfflineMap.Deleted -> {
            title = stringResource(R.string.offline_map_deleted)
            detail = stringResource(R.string.offline_map_deleted_detail)
            icon = R.drawable.ic_map
        }
    }
    Column(Modifier.fillMaxWidth().clip(CardShape).background(colors.surfaceContainer)) {
        MapPicture(faded = map !is OfflineMap.Ready, Modifier.fillMaxWidth().height(PictureHeight))
        Column(Modifier.padding(start = 16.dp, top = 14.dp, end = 16.dp, bottom = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(
                // Said once it changes, but not for each percent of a download.
                Modifier.semantics(mergeDescendants = true) { if (map !is OfflineMap.Downloading) liveRegion = LiveRegionMode.Polite },
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(painterResource(icon), contentDescription = null, tint = tint, modifier = Modifier.size(24.dp))
                Column {
                    Text(title, style = MaterialTheme.typography.titleMedium)
                    detail?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = colors.onSurfaceVariant) }
                }
            }
            if (map is OfflineMap.Ready) {
                map.againFailed?.let { AgainFailed(it, size) }
                if (map.deleteFailed) {
                    Problem(stringResource(R.string.offline_map_delete_failed), stringResource(R.string.offline_map_delete_failed_detail))
                }
            }
            if (map is OfflineMap.Downloading) {
                LinearProgressIndicator(
                    progress = { map.percent / 100f },
                    modifier = Modifier.fillMaxWidth(),
                    color = colors.primary,
                    trackColor = colors.surfaceContainerHighest,
                )
            }
        }
    }
}

/** Why the new copy the driver asked for did not come. The map here stays, and Get it again tries again. */
@Composable
private fun AgainFailed(failure: MapFileState.Failed, size: (Long) -> String) {
    val detail = when (failure.reason) {
        MapFailure.NO_SPACE ->
            failure.size?.let { stringResource(R.string.offline_map_again_no_space, size(it)) } ?: stringResource(R.string.offline_map_again_no_space_unknown)
        MapFailure.NETWORK -> stringResource(R.string.offline_map_again_no_connection)
        MapFailure.INVALID_FILE -> stringResource(R.string.offline_map_again_unusable)
        MapFailure.CLEARTEXT_BLOCKED -> stringResource(R.string.offline_map_again_needs_https)
        MapFailure.UNEXPECTED -> stringResource(R.string.offline_map_again_unexpected)
    }
    Problem(stringResource(R.string.offline_map_again_failed), detail)
}

/** Under Ready: something the driver asked for that did not happen, with [title] over why. */
@Composable
private fun Problem(title: String, detail: String) {
    Row(
        Modifier.semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite },
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(painterResource(R.drawable.ic_error), contentDescription = null, tint = AppTheme.problemColors.iconOnSurface, modifier = Modifier.size(24.dp))
        Column {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(detail, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** Size, the day it was fetched, and when the app last asked about it. */
@Composable
private fun Facts(details: MapDetails, nowEpochSec: Long) {
    val locale = LocalLocale.current.platformLocale
    val zone = ZoneId.systemDefault()
    val today = stringResource(R.string.offline_map_today)
    Column {
        Fact(R.string.offline_map_size, Formatter.formatShortFileSize(LocalContext.current, details.bytes), GroupPlace.FIRST)
        Fact(R.string.offline_map_fetched, fetchedOn(details.fetchedAtMs, zone, locale), GroupPlace.MIDDLE)
        Fact(
            R.string.offline_map_last_checked,
            checkedAt(details.checkedAtEpochSec, nowEpochSec, zone, locale) { time -> today.format(locale, time) },
            GroupPlace.LAST,
        )
    }
}

@Composable
private fun Fact(name: Int, value: String, place: GroupPlace) {
    ListItem(
        headlineContent = { Text(stringResource(name)) },
        trailingContent = { Text(value, style = MaterialTheme.typography.bodyMedium) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(place).semantics(mergeDescendants = true) {},
    )
}

/** At most two: the one that puts things right, or gets the map, and Delete for a map that is here. */
@Composable
private fun Actions(map: OfflineMap, onGetAgain: () -> Unit, onAskDelete: () -> Unit, onTryAgain: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val tonal = ButtonDefaults.buttonColors(containerColor = colors.primaryContainer, contentColor = colors.onPrimaryContainer)
    val wide = Modifier.fillMaxWidth().heightIn(min = 48.dp)
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        when (map) {
            is OfflineMap.Ready -> {
                Button(onClick = onGetAgain, colors = tonal, modifier = wide) { Text(stringResource(R.string.offline_map_get_again)) }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center) {
                    QuietButton(stringResource(R.string.offline_map_delete), onClick = onAskDelete)
                }
            }
            is OfflineMap.NoSpace -> Button(onClick = onTryAgain, colors = tonal, modifier = wide) { Text(stringResource(R.string.offline_map_try_again)) }
            is OfflineMap.NotDownloaded -> Button(onClick = onTryAgain, colors = tonal, modifier = wide) { Text(stringResource(R.string.offline_map_try_again_now)) }
            OfflineMap.Deleted -> Button(onClick = onGetAgain, modifier = wide) { Text(stringResource(R.string.offline_map_get)) }
            is OfflineMap.Downloading, is OfflineMap.Unusable -> Unit
        }
    }
}

private fun lineOf(map: OfflineMap): Int = when (map) {
    is OfflineMap.Ready -> R.string.offline_map_line_ready
    is OfflineMap.Downloading -> R.string.offline_map_line_downloading
    is OfflineMap.NoSpace -> R.string.offline_map_line_no_space
    is OfflineMap.NotDownloaded -> R.string.offline_map_line_not_downloaded
    is OfflineMap.Unusable -> R.string.offline_map_line_unusable
    OfflineMap.Deleted -> R.string.offline_map_line_deleted
}

/** A few streets, a park and a route along them, in the Tracking map's colours. Nothing in it is read out. */
@Composable
private fun MapPicture(faded: Boolean, modifier: Modifier) {
    val dark = isSystemInDarkTheme()
    val streets = streetColors(dark)
    val route = MaterialTheme.colorScheme.primary
    val casing = Color(casingColor(route.toArgb(), dark))
    Canvas(modifier.alpha(if (faded) FADED else 1f)) {
        val w = size.width
        val h = size.height
        drawRect(streets.land)
        drawRect(streets.park, topLeft = Offset(w * 0.47f, h * 0.27f), size = Size(w * 0.17f, h * 0.17f))
        val road = 6.dp.toPx()
        for (x in listOf(0.15f, 0.42f, 0.68f, 0.92f)) drawLine(streets.road, Offset(w * x, 0f), Offset(w * x, h), road)
        for (y in listOf(0.18f, 0.5f, 0.84f)) drawLine(streets.road, Offset(0f, h * y), Offset(w, h * y), road)
        val path = Path().apply {
            moveTo(w * 0.15f, h * 1.1f)
            lineTo(w * 0.15f, h * 0.5f)
            lineTo(w * 0.68f, h * 0.5f)
            lineTo(w * 0.68f, -h * 0.1f)
        }
        drawPath(path, casing, style = Stroke(11.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round))
        drawPath(path, route, style = Stroke(8.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round))
    }
}
