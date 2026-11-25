@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.help

import androidx.annotation.DrawableRes
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.GroupTitle
import org.onebusaway.vehicletracker.ui.components.groupRow
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tracking.minutesText
import org.onebusaway.vehicletracker.ui.tracking.waitingWords

private val DiscSize = 40.dp
private const val DISC_ALPHA = 0.16f

/**
 * Help, opened from Settings: the welcome again, and what each line on Tracking means. Nothing on it moves by itself.
 * [onWelcomeAgain] opens the welcome, which comes back here.
 */
@Composable
fun HelpScreen(onBack: () -> Unit, onWelcomeAgain: () -> Unit) {
    // Pinned: the bar stays put and takes the container colour once the page is under it.
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        TopAppBar(
            title = { Text(stringResource(R.string.help_title), modifier = Modifier.semantics { heading() }) },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
                }
            },
            scrollBehavior = scrollBehavior,
        )
        LazyColumn(contentPadding = PaddingValues(start = 16.dp, top = 8.dp, end = 16.dp, bottom = 32.dp)) {
            items(helpPage, key = { it.key }, contentType = { it.contentType }) { item ->
                when (item) {
                    HelpItem.WelcomeAgain -> WelcomeAgainRow(onWelcomeAgain)
                    is HelpItem.Section -> GroupTitle(item.section.title)
                    is HelpItem.Status -> StatusRow(item.help, item.place)
                    is HelpItem.Problem -> ProblemRow(item.help, item.place)
                }
            }
        }
    }
}

@Composable
private fun WelcomeAgainRow(onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    ListItem(
        headlineContent = { Text(stringResource(R.string.help_welcome_again)) },
        supportingContent = { Text(stringResource(R.string.help_welcome_again_detail)) },
        leadingContent = { Disc(R.drawable.ic_play_circle, colors.onPrimaryContainer.copy(alpha = DISC_ALPHA), colors.onPrimaryContainer) },
        trailingContent = { Icon(painterResource(R.drawable.ic_chevron_right), contentDescription = null) },
        colors = ListItemDefaults.colors(
            containerColor = colors.primaryContainer,
            headlineColor = colors.onPrimaryContainer,
            supportingColor = colors.onPrimaryContainer,
            trailingIconColor = colors.onPrimaryContainer,
        ),
        modifier = Modifier.groupRow(GroupPlace.ONLY).clickable(role = Role.Button, onClick = onClick),
    )
}

@Composable
private fun StatusRow(help: CardStatusHelp, place: GroupPlace) {
    val title = when (val words = help.words) {
        is CardWords.Minutes -> minutesText(words.minutes)
        is CardWords.Text -> stringResource(words.id)
        is CardWords.Waiting -> pluralStringResource(waitingWords(words.outlook), words.count, words.count)
    }
    val tone = AppTheme.statusColors[help.tone]
    GroupRow(title, stringResource(help.meaning), place) { Disc(help.icon, tone.fill, tone.onFill) }
}

@Composable
private fun ProblemRow(help: ProblemHelp, place: GroupPlace) {
    val colors = AppTheme.problemColors
    GroupRow(stringResource(help.title), stringResource(help.meaning), place) {
        Disc(help.icon, colors.attentionContainer, colors.iconOnSurface)
    }
}

/** A row of a status or problem group: its title over its meaning, which ListItem has TalkBack read as one. */
@Composable
private fun GroupRow(title: String, meaning: String, place: GroupPlace, leading: @Composable () -> Unit) {
    ListItem(
        headlineContent = { Text(title) },
        supportingContent = { Text(meaning) },
        leadingContent = leading,
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(place),
    )
}

/** The status card's icon on its disc. */
@Composable
private fun Disc(@DrawableRes icon: Int, disc: Color, tint: Color) {
    Box(Modifier.size(DiscSize).background(disc, CircleShape), contentAlignment = Alignment.Center) {
        Icon(painterResource(icon), contentDescription = null, tint = tint, modifier = Modifier.size(24.dp))
    }
}
