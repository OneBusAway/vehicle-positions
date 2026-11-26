@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.settings

import android.content.ActivityNotFoundException
import android.content.Intent
import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.PrivacyPolicySource
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.GroupTitle
import org.onebusaway.vehicletracker.ui.components.groupRow
import org.onebusaway.vehicletracker.ui.isolated
import org.onebusaway.vehicletracker.ui.login.serverLabel
import javax.inject.Inject

private const val TAG = "PrivacyScreen"

/** The server signed in, as Privacy names it, and the agency's privacy policy link. */
@HiltViewModel
class PrivacyViewModel @Inject constructor(sessionStore: SessionStore, policy: PrivacyPolicySource) : ViewModel() {
    val server: StateFlow<String?> = sessionStore.session.map { it.serverUrl?.let(::serverLabel) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** Null until the server has answered, and when there is no link to open. */
    val policyLink: StateFlow<String?> = flow { emit(policy.link()) }.stateIn(viewModelScope, SharingStarted.Eagerly, null)
}

/** In plain words: what the app sends, when and to whom, and what it keeps on the phone. */
@Composable
fun PrivacyScreen(onBack: () -> Unit, viewModel: PrivacyViewModel = hiltViewModel()) {
    val server by viewModel.server.collectAsStateWithLifecycle()
    val link by viewModel.policyLink.collectAsStateWithLifecycle()
    val context = LocalContext.current
    PrivacyContent(server, link, onBack) { url ->
        // The browser, never a page in the app: the agency's policy is theirs.
        val open = Intent(Intent.ACTION_VIEW, url.toUri()).addCategory(Intent.CATEGORY_BROWSABLE)
        try {
            context.startActivity(open)
        } catch (e: ActivityNotFoundException) {
            AppLog.w(TAG, "No browser to open the privacy policy", e)
        }
    }
}

@Composable
internal fun PrivacyContent(server: String?, policyLink: String?, onBack: () -> Unit, onOpenPolicy: (String) -> Unit) {
    // Pinned: the bar stays put and takes the container colour once the page is under it.
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        SettingsPageBar(R.string.privacy_title, onBack, scrollBehavior)
        Column(Modifier.verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, bottom = 32.dp)) {
            GroupTitle(R.string.privacy_group_sent)
            Line(R.drawable.ic_schedule, R.string.privacy_trip, stringResource(R.string.privacy_trip_detail), GroupPlace.FIRST)
            Line(R.drawable.ic_near_me, R.string.privacy_position, stringResource(R.string.privacy_position_detail), GroupPlace.MIDDLE)
            Line(R.drawable.ic_groups, R.string.privacy_occupancy, stringResource(R.string.privacy_occupancy_detail), GroupPlace.LAST)
            GroupTitle(R.string.privacy_group_who)
            Line(
                R.drawable.ic_dns,
                R.string.privacy_agency,
                server?.let { stringResource(R.string.privacy_agency_detail, isolated(it)) } ?: stringResource(R.string.privacy_agency_detail_no_server),
                GroupPlace.FIRST,
            )
            Line(R.drawable.ic_emoji_people, R.string.privacy_riders, stringResource(R.string.privacy_riders_detail), GroupPlace.LAST)
            GroupTitle(R.string.privacy_group_kept)
            Line(R.drawable.ic_account_circle, R.string.privacy_account, stringResource(R.string.privacy_account_detail), GroupPlace.FIRST)
            Line(R.drawable.ic_cloud_off, R.string.privacy_kept, stringResource(R.string.privacy_kept_detail), GroupPlace.LAST)
            // Not there without a link.
            if (policyLink != null) {
                GroupTitle(R.string.privacy_group_more)
                ListItem(
                    headlineContent = { Text(stringResource(R.string.privacy_policy)) },
                    supportingContent = { Text(stringResource(R.string.privacy_policy_detail)) },
                    leadingContent = { Icon(painterResource(R.drawable.ic_policy), contentDescription = null) },
                    trailingContent = { Icon(painterResource(R.drawable.ic_open_in_new), contentDescription = null) },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
                    modifier = Modifier.groupRow(GroupPlace.ONLY).clickable(role = Role.Button) { onOpenPolicy(policyLink) },
                )
            }
        }
    }
}

/** One thing Privacy says: a symbol, and its words. Nothing to tap. */
@Composable
private fun Line(@DrawableRes icon: Int, @StringRes title: Int, detail: String, place: GroupPlace) {
    ListItem(
        headlineContent = { Text(stringResource(title)) },
        supportingContent = { Text(detail) },
        leadingContent = { Icon(painterResource(icon), contentDescription = null) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(place).semantics(mergeDescendants = true) {},
    )
}

/** The bar of a page Settings opens: Back and its title, which is a heading. */
@Composable
internal fun SettingsPageBar(@StringRes title: Int, onBack: () -> Unit, scrollBehavior: androidx.compose.material3.TopAppBarScrollBehavior) =
    SettingsPageBar(stringResource(title), onBack, scrollBehavior)

@Composable
internal fun SettingsPageBar(title: String, onBack: () -> Unit, scrollBehavior: androidx.compose.material3.TopAppBarScrollBehavior) {
    TopAppBar(
        title = { Text(title, modifier = Modifier.semantics { heading() }) },
        navigationIcon = {
            IconButton(onClick = onBack) {
                Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
            }
        },
        scrollBehavior = scrollBehavior,
    )
}
