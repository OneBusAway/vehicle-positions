@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.help

import android.content.Intent
import androidx.annotation.DrawableRes
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tracking.minutesText
import java.util.concurrent.atomic.AtomicBoolean

// A group's rows are joined: round on the group's outside, tight where two rows meet.
private val OuterCorner = 20.dp
private val InnerCorner = 4.dp
private val RowGap = 3.dp
private val DiscSize = 40.dp
private const val DISC_ALPHA = 0.16f

private val WelcomeRowShape = RoundedCornerShape(OuterCorner)
private val GroupPlace.shape: RoundedCornerShape
    get() = when (this) {
        GroupPlace.FIRST -> RoundedCornerShape(OuterCorner, OuterCorner, InnerCorner, InnerCorner)
        GroupPlace.MIDDLE -> RoundedCornerShape(InnerCorner)
        GroupPlace.LAST -> RoundedCornerShape(InnerCorner, InnerCorner, OuterCorner, OuterCorner)
    }

/**
 * Help, opened from the account menu: what each line on Tracking means, the welcome again, the build and the server,
 * and Share diagnostics. Nothing on it moves by itself. [onWelcomeAgain] opens the welcome, which comes back here.
 */
@Composable
fun HelpScreen(onBack: () -> Unit, onWelcomeAgain: () -> Unit, viewModel: HelpViewModel = hiltViewModel()) {
    val about by viewModel.about.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    // A second tap while the text is being read would open a second share sheet.
    val sharing = remember { AtomicBoolean(false) }
    HelpContent(
        about = about,
        onBack = onBack,
        onWelcomeAgain = onWelcomeAgain,
        onShare = {
            if (sharing.compareAndSet(false, true)) {
                scope.launch {
                    try {
                        context.startActivity(shareIntent(viewModel.diagnostics()))
                    } finally {
                        sharing.set(false)
                    }
                }
            }
        },
    )
}

/**
 * The system share sheet with [text] in it, which it shows above the apps to send it to. The driver chooses where it
 * goes; the app sends nothing itself.
 */
internal fun shareIntent(text: String): Intent {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, text)
    }
    return Intent.createChooser(send, null)
}

@Composable
internal fun HelpContent(about: HelpAbout, onBack: () -> Unit, onWelcomeAgain: () -> Unit, onShare: () -> Unit) {
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
                    is HelpItem.Section -> SectionTitle(item.section)
                    is HelpItem.Status -> StatusRow(item.help, item.place)
                    is HelpItem.Problem -> ProblemRow(item.help, item.place)
                    is HelpItem.About -> AboutRow(item.fact, about, item.place)
                    HelpItem.Share -> ShareRow(onShare)
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
        modifier = Modifier.clip(WelcomeRowShape).clickable(role = Role.Button, onClick = onClick),
    )
}

@Composable
private fun SectionTitle(section: HelpSection) {
    Text(
        text = stringResource(section.title),
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier
            .padding(start = 4.dp, top = 24.dp, bottom = 8.dp)
            .semantics { heading() },
    )
}

@Composable
private fun StatusRow(help: CardStatusHelp, place: GroupPlace) {
    val title = when (val words = help.words) {
        is CardWords.Minutes -> minutesText(words.minutes)
        is CardWords.Text -> stringResource(words.id)
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

@Composable
private fun AboutRow(fact: AboutFact, about: HelpAbout, place: GroupPlace) {
    val value = when (fact) {
        AboutFact.VERSION -> about.version
        AboutFact.SERVER -> about.server
    }
    ListItem(
        // The value takes what the label leaves, and wraps there at large text rather than squeezing the label.
        headlineContent = {
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(stringResource(fact.label))
                Text(
                    text = value,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.End,
                    modifier = Modifier.weight(1f),
                )
            }
        },
        leadingContent = { Icon(painterResource(fact.icon), contentDescription = null) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(place),
    )
}

@Composable
private fun ShareRow(onShare: () -> Unit) {
    Column(Modifier.padding(top = 24.dp)) {
        OutlinedButton(onClick = onShare, modifier = Modifier.fillMaxWidth().heightIn(min = 56.dp)) {
            Icon(painterResource(R.drawable.ic_share), contentDescription = null, modifier = Modifier.size(18.dp))
            Text(stringResource(R.string.help_share), modifier = Modifier.padding(start = 8.dp))
        }
        Text(
            text = stringResource(R.string.help_share_detail),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(start = 4.dp, top = 12.dp, end = 4.dp),
        )
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

private fun Modifier.groupRow(place: GroupPlace): Modifier =
    padding(top = if (place == GroupPlace.FIRST) 0.dp else RowGap).clip(place.shape)

/** The status card's icon on its disc. */
@Composable
private fun Disc(@DrawableRes icon: Int, disc: Color, tint: Color) {
    Box(Modifier.size(DiscSize).background(disc, CircleShape), contentAlignment = Alignment.Center) {
        Icon(painterResource(icon), contentDescription = null, tint = tint, modifier = Modifier.size(24.dp))
    }
}
