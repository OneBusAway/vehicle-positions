@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.settings

import androidx.annotation.StringRes
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.ScreenOn
import org.onebusaway.vehicletracker.data.TripSettings
import org.onebusaway.vehicletracker.ui.components.GroupOuterCorner
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.GroupTitle
import org.onebusaway.vehicletracker.ui.components.groupPlace
import org.onebusaway.vehicletracker.ui.components.groupRow
import org.onebusaway.vehicletracker.ui.map.casingColor
import org.onebusaway.vehicletracker.ui.map.streetColors
import org.onebusaway.vehicletracker.ui.theme.AppTheme

private val CardShape = RoundedCornerShape(GroupOuterCorner)
private val PictureHeight = 110.dp
private val CardGap = 12.dp

/** From this text size up, as on a narrow phone, the map's two cards are under each other. */
private const val STACKS_FROM_TEXT = 1.3f

/** How far round the pictured route runs from north, in degrees. */
private const val ROUTE_FROM_NORTH = 40f

/** What Tracking does during a trip, as the driver likes it: the display, the map, and a vibration when reporting has a problem. */
@Composable
fun TripSettingsScreen(onBack: () -> Unit, viewModel: TripSettingsViewModel = hiltViewModel()) {
    val settings by viewModel.settings.collectAsStateWithLifecycle()
    TripSettingsContent(settings, onBack, viewModel::onScreenOn, viewModel::onMapUp, viewModel::onVibrates)
}

@Composable
internal fun TripSettingsContent(
    settings: TripSettings,
    onBack: () -> Unit,
    onScreenOn: (ScreenOn) -> Unit,
    onMapUp: (MapUp) -> Unit,
    onVibrates: (Boolean) -> Unit,
) {
    // Pinned: the bar stays put and takes the container colour once the page is under it.
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        TopAppBar(
            title = { Text(stringResource(R.string.trip_settings_title), modifier = Modifier.semantics { heading() }) },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
                }
            },
            scrollBehavior = scrollBehavior,
        )
        Column(Modifier.verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, bottom = 32.dp)) {
            GroupTitle(R.string.trip_screen)
            Column(Modifier.selectableGroup()) {
                ScreenOn.entries.forEachIndexed { index, choice ->
                    ScreenRow(choice, choice == settings.screenOn, groupPlace(index, ScreenOn.entries.size)) { onScreenOn(choice) }
                }
            }
            GroupTitle(R.string.trip_map)
            MapCards(settings.mapUp, onMapUp)
            Spacer(Modifier.height(24.dp))
            VibrateRow(settings.vibrates, onVibrates)
        }
    }
}

@Composable
private fun ScreenRow(choice: ScreenOn, selected: Boolean, place: GroupPlace, onClick: () -> Unit) {
    val (title, detail) = when (choice) {
        ScreenOn.CHARGING -> R.string.trip_screen_charging to R.string.trip_screen_charging_detail
        ScreenOn.ALWAYS -> R.string.trip_screen_always to R.string.trip_screen_always_detail
    }
    ListItem(
        headlineContent = { Text(stringResource(title)) },
        supportingContent = { Text(stringResource(detail)) },
        leadingContent = { RadioButton(selected = selected, onClick = null) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(place).selectable(selected = selected, role = Role.RadioButton, onClick = onClick),
    )
}

/** Side by side, or under each other where their words would be squeezed. */
@Composable
private fun MapCards(chosen: MapUp, onChoose: (MapUp) -> Unit) {
    if (isNarrow() || LocalDensity.current.fontScale >= STACKS_FROM_TEXT) {
        Column(Modifier.selectableGroup(), verticalArrangement = Arrangement.spacedBy(CardGap)) {
            for (choice in MapUp.entries) MapCard(choice, choice == chosen, { onChoose(choice) }, Modifier.fillMaxWidth())
        }
    } else {
        Row(Modifier.height(IntrinsicSize.Max).selectableGroup(), horizontalArrangement = Arrangement.spacedBy(CardGap)) {
            for (choice in MapUp.entries) MapCard(choice, choice == chosen, { onChoose(choice) }, Modifier.weight(1f).fillMaxHeight())
        }
    }
}

@StringRes
private fun nameOf(choice: MapUp): Int = when (choice) {
    MapUp.HEADING -> R.string.trip_map_heading
    MapUp.NORTH -> R.string.trip_map_north
}

/** Its picture says what its words do, so only the words are read out. */
@Composable
private fun MapCard(choice: MapUp, selected: Boolean, onClick: () -> Unit, modifier: Modifier) {
    val colors = MaterialTheme.colorScheme
    Column(
        modifier
            .clip(CardShape)
            .background(colors.surfaceContainer)
            .then(if (selected) Modifier.border(2.dp, colors.primary, CardShape) else Modifier)
            .selectable(selected = selected, role = Role.RadioButton, onClick = onClick),
    ) {
        MapPicture(choice, Modifier.fillMaxWidth().height(PictureHeight))
        Row(
            modifier = Modifier.fillMaxWidth().heightIn(min = 56.dp).padding(horizontal = 12.dp, vertical = 10.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RadioButton(selected = selected, onClick = null)
            Text(stringResource(nameOf(choice)), style = MaterialTheme.typography.bodyLarge)
        }
    }
}

/** The same streets and route as the map would show them: turned to the way the vehicle goes, or north up. */
@Composable
private fun MapPicture(choice: MapUp, modifier: Modifier) {
    val dark = isSystemInDarkTheme()
    val streets = streetColors(dark)
    val route = MaterialTheme.colorScheme.primary
    val casing = Color(casingColor(route.toArgb(), dark))
    val ring = AppTheme.statusColors.onTime.text
    val vehicle = painterResource(R.drawable.ic_map_vehicle_puck)
    Canvas(modifier) {
        val centre = Offset(size.width / 2, size.height * 0.62f)
        val reach = size.width
        clipRect {
            drawRect(streets.land)
            rotate(if (choice == MapUp.HEADING) -ROUTE_FROM_NORTH else 0f, centre) {
                val gap = 38.dp.toPx()
                for (street in -6..6) {
                    val away = (street + 0.5f) * gap
                    drawLine(streets.road, centre + Offset(away, -2 * reach), centre + Offset(away, 2 * reach), 6.dp.toPx())
                    drawLine(streets.road, centre + Offset(-2 * reach, away), centre + Offset(2 * reach, away), 6.dp.toPx())
                }
            }
            val pointing = if (choice == MapUp.HEADING) 0f else ROUTE_FROM_NORTH
            rotate(pointing, centre) {
                val from = centre + Offset(0f, 2 * reach)
                val to = centre + Offset(0f, -2 * reach)
                drawLine(casing, from, to, 11.dp.toPx(), StrokeCap.Round)
                drawLine(route, from, to, 8.dp.toPx(), StrokeCap.Round)
            }
            drawVehicle(vehicle, centre, 34.dp.toPx(), ring, pointing)
        }
    }
}

@Composable
private fun VibrateRow(on: Boolean, onChange: (Boolean) -> Unit) {
    ListItem(
        headlineContent = { Text(stringResource(R.string.trip_vibrate)) },
        supportingContent = { Text(stringResource(R.string.trip_vibrate_detail)) },
        leadingContent = { Icon(painterResource(R.drawable.ic_vibration), contentDescription = null) },
        trailingContent = { Switch(checked = on, onCheckedChange = null) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(GroupPlace.ONLY).toggleable(value = on, role = Role.Switch, onValueChange = onChange),
    )
}
