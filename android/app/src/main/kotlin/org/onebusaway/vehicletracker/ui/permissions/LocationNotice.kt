package org.onebusaway.vehicletracker.ui.permissions

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.isTraversalGroup
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.LocationNoticeStore
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.theme.fadeThroughIn
import org.onebusaway.vehicletracker.ui.theme.fadeThroughOut
import org.onebusaway.vehicletracker.ui.login.serverLabel
import org.onebusaway.vehicletracker.ui.settings.SettingsRow
import java.io.IOException
import javax.inject.Inject

private const val TAG = "LocationNotice"

/** Whether this phone has agreed to the location notice, and the server the notice names. */
@HiltViewModel
class LocationNoticeViewModel @Inject constructor(sessionStore: SessionStore, private val store: LocationNoticeStore) : ViewModel() {
    private val agreedNow = MutableStateFlow(false)

    /** Null until read. A tap on Agree counts at once, whether or not the phone could save it. */
    val agreed: StateFlow<Boolean?> = combine(store.agreed, agreedNow) { saved, now -> saved || now }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    val server: StateFlow<String?> = sessionStore.session.map { it.serverUrl?.let(::serverLabel) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    fun agree() {
        agreedNow.value = true
        viewModelScope.launch {
            try {
                store.agree()
            } catch (e: IOException) {
                // The driver agreed, so Android is asked this time; the notice shows again next time.
                AppLog.w(TAG, "Could not save the agreement to the location notice", e)
            }
        }
    }
}

/** The notice as a screen that may show it reads it: [agreed] null until read. */
@Stable
class LocationNoticeState(val agreed: Boolean?, val server: String?, val agree: () -> Unit)

@Composable
fun rememberLocationNotice(viewModel: LocationNoticeViewModel = hiltViewModel()): LocationNoticeState {
    val agreed by viewModel.agreed.collectAsStateWithLifecycle()
    val server by viewModel.server.collectAsStateWithLifecycle()
    return LocationNoticeState(agreed, server, viewModel::agree)
}

/**
 * [content] with the notice over it while [visible]. Only the notice can be tapped or read by TalkBack then, and Back
 * is Not now, ahead of anything [content] does with Back.
 */
@Composable
internal fun LocationNoticeHost(
    visible: Boolean,
    server: String?,
    onAgree: () -> Unit,
    onNotNow: () -> Unit,
    content: @Composable () -> Unit,
) {
    Box {
        Box(if (visible) Modifier.clearAndSetSemantics {} else Modifier) { content() }
        AnimatedVisibility(visible = visible, enter = fadeThroughIn(), exit = fadeThroughOut()) {
            LocationNoticeContent(server, onAgree, onNotNow)
        }
    }
    // After the content's own, so it comes first.
    BackHandler(enabled = visible, onBack = onNotNow)
}

/**
 * What Google Play wants said before Android is asked for location, background location above all: that the app
 * collects it, what for, and that it does so when the app is closed. Only a tap on Agree agrees; Not now and Back ask
 * nothing, and it shows again next time.
 */
@Composable
internal fun LocationNoticeContent(server: String?, onAgree: () -> Unit, onNotNow: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    // Over the checklist or a Settings page it covers the whole window: a Surface, so a tap that misses its buttons
    // stops here rather than reach the page under it. It keeps clear of the bars itself.
    Surface(Modifier.fillMaxSize().semantics { isTraversalGroup = true }) {
        Column(Modifier.fillMaxSize().safeDrawingPadding()) {
            IconButton(onClick = onNotNow, modifier = Modifier.padding(start = 4.dp, top = 4.dp)) {
                Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
            }
            Column(
                Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = 24.dp),
            ) {
                Box(
                    Modifier.padding(top = 8.dp).size(64.dp).clip(CircleShape).background(colors.primaryContainer),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(painterResource(R.drawable.ic_location_on), contentDescription = null, tint = colors.onPrimaryContainer, modifier = Modifier.size(32.dp))
                }
                Text(
                    text = stringResource(R.string.location_notice_title),
                    style = MaterialTheme.typography.headlineMedium,
                    modifier = Modifier.padding(top = 24.dp).semantics { heading() },
                )
                Text(
                    text = stringResource(R.string.location_notice_text, stringResource(R.string.app_name)),
                    style = MaterialTheme.typography.bodyLarge,
                    color = colors.onSurfaceVariant,
                    modifier = Modifier.padding(top = 16.dp, bottom = 24.dp),
                )
                SettingsRow(R.drawable.ic_schedule, stringResource(R.string.location_notice_when), stringResource(R.string.location_notice_when_detail), GroupPlace.FIRST)
                SettingsRow(R.drawable.ic_near_me, stringResource(R.string.location_notice_what), stringResource(R.string.location_notice_what_detail), GroupPlace.MIDDLE)
                SettingsRow(
                    R.drawable.ic_dns,
                    server?.let { stringResource(R.string.location_notice_to, it) } ?: stringResource(R.string.location_notice_to_agency),
                    stringResource(R.string.location_notice_to_detail),
                    GroupPlace.LAST,
                )
            }
            Column(
                Modifier.fillMaxWidth().padding(start = 24.dp, top = 12.dp, end = 24.dp, bottom = 16.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Button(onClick = onAgree, modifier = Modifier.fillMaxWidth().heightIn(min = 56.dp)) {
                    Text(stringResource(R.string.location_notice_agree))
                }
                TextButton(onClick = onNotNow, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) {
                    Text(stringResource(R.string.location_notice_not_now))
                }
            }
        }
    }
}
