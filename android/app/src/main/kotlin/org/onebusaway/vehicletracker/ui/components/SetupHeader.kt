package org.onebusaway.vehicletracker.ui.components

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ProvideTextStyle
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.isTraversalGroup
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.routes.RouteBadge
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.theme.AppMotion

// AssistChip's own shape and height, which the route's chip matches.
private val ChipShape = RoundedCornerShape(8.dp)
private val ChipHeight = 32.dp

// With no Back button, this lines the first chip up with the title's 24 dp inset.
private val NoBackInset = 14.dp

/**
 * The top of the route and run steps: Back, then the trip so far as chips that each go back to their own step, then
 * the step the driver is on. [onBack] is null on a first step, which has nothing to go back to, and [onBusClick] is
 * null when the driver's only bus was chosen for them: the chip still says which bus it is, but is not a button.
 */
@Composable
internal fun SetupHeader(
    busId: String,
    step: String,
    onBack: (() -> Unit)?,
    onBusClick: (() -> Unit)?,
    modifier: Modifier = Modifier,
    route: RouteBadgeModel? = null,
    onRouteClick: () -> Unit = {},
) {
    Row(
        modifier = modifier.fillMaxWidth().height(56.dp).padding(start = 4.dp, end = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (onBack != null) {
            SetupBackButton(onBack)
        } else {
            Spacer(Modifier.width(NoBackInset))
        }
        val tripSoFar = stringResource(R.string.setup_trip_so_far)
        Row(
            modifier = Modifier.weight(1f).semantics {
                isTraversalGroup = true
                contentDescription = tripSoFar
            },
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            // Weighted, so a long fleet number is what gets cut short, not the route or the step.
            BusChip(busId, onBusClick, Modifier.weight(1f, fill = false))
            Chevron()
            if (route != null) {
                RouteChip(route, onRouteClick)
                Chevron()
            }
            Text(
                text = step,
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** The setup screens' Back, also on the checklist before a trip, which has no trip so far to show beside it. */
@Composable
internal fun SetupBackButton(onBack: () -> Unit) {
    IconButton(onClick = onBack) {
        Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
    }
}

@Composable
private fun BusChip(busId: String, onClick: (() -> Unit)?, modifier: Modifier) {
    val icon = @Composable {
        Icon(
            painter = painterResource(R.drawable.ic_directions_bus),
            contentDescription = stringResource(R.string.setup_bus),
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(AssistChipDefaults.IconSize),
        )
    }
    val label = @Composable { Text(busId, maxLines = 1, overflow = TextOverflow.Ellipsis) }
    if (onClick != null) {
        AssistChip(onClick = onClick, label = label, leadingIcon = icon, modifier = modifier)
    } else {
        // AssistChip's look and spacing, without the button.
        Row(
            modifier = modifier
                .height(ChipHeight)
                .border(1.dp, MaterialTheme.colorScheme.outlineVariant, ChipShape)
                .padding(start = 8.dp, end = 16.dp)
                .semantics(mergeDescendants = true) {},
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            icon()
            ProvideTextStyle(MaterialTheme.typography.labelLarge, label)
        }
    }
}

@Composable
private fun RouteChip(route: RouteBadgeModel, onClick: () -> Unit) {
    val description = stringResource(R.string.setup_route, route.shortName)
    RouteBadge(
        route = route,
        modifier = Modifier
            .minimumInteractiveComponentSize()
            .clip(ChipShape)
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { contentDescription = description }
            .height(ChipHeight)
            .defaultMinSize(minWidth = ChipHeight),
        shape = ChipShape,
    )
}

@Composable
private fun Chevron() {
    Icon(
        painter = painterResource(R.drawable.ic_chevron_right),
        contentDescription = null,
        tint = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.size(20.dp),
    )
}

/**
 * A hairline under a pinned header that fades in once content has scrolled under it. [scrolledUnder] is read through
 * derivedStateOf, so scrolling recomposes nothing until it flips, and the fade is applied when drawing.
 */
@Composable
internal fun ScrolledUnderDivider(scrolledUnder: () -> Boolean, modifier: Modifier = Modifier) {
    val shown by remember(scrolledUnder) { derivedStateOf(scrolledUnder) }
    val alpha by animateFloatAsState(if (shown) 1f else 0f, AppMotion.defaultEffectsSpec(), label = "divider")
    HorizontalDivider(modifier.graphicsLayer { this.alpha = alpha }, color = MaterialTheme.colorScheme.outlineVariant)
}
