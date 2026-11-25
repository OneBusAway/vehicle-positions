package org.onebusaway.vehicletracker.ui.settings

import android.content.Intent
import androidx.activity.compose.LocalActivity
import androidx.annotation.DrawableRes
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.takeOrElse
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.ui.account.AccountUiState
import org.onebusaway.vehicletracker.ui.account.AccountViewModel
import org.onebusaway.vehicletracker.ui.account.SignOutDialog
import org.onebusaway.vehicletracker.ui.components.GroupOuterCorner
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.GroupTitle
import org.onebusaway.vehicletracker.ui.components.groupRow
import org.onebusaway.vehicletracker.ui.isolated
import org.onebusaway.vehicletracker.ui.language.LanguageChooser
import org.onebusaway.vehicletracker.ui.permissions.Fix
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.fixOf
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFixes
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.util.concurrent.atomic.AtomicBoolean

private val AccountShape = RoundedCornerShape(GroupOuterCorner)

/** From this text size up, as on a narrow phone, Sign out goes under the email rather than beside it. */
private const val STACKS_FROM_TEXT = 1.3f

/**
 * Settings, opened from the account button: whether this phone can report a trip, who is signed in and Sign out, what the
 * app is allowed, its language and theme, Help and the build. [onSignedOut] is the phone having forgotten the driver.
 */
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onPermissionCheck: () -> Unit,
    onHelp: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: SettingsViewModel = hiltViewModel(),
    accountViewModel: AccountViewModel = hiltViewModel(),
) {
    val account by accountViewModel.uiState.collectAsStateWithLifecycle()
    val version by viewModel.version.collectAsStateWithLifecycle()
    val waiting by viewModel.waiting.collectAsStateWithLifecycle()
    val theme by viewModel.theme.collectAsStateWithLifecycle()
    val activity = LocalActivity.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    // A second tap while the text is being read would open a second share sheet.
    val sharing = remember { AtomicBoolean(false) }

    val access = rememberPhoneAccess()
    val fixes = rememberPermissionFixes(onAnswer = access::refresh)
    val readiness = readiness(access.value, waiting)
    SettingsContent(
        readiness = readiness,
        locationFix = fixOf(PermissionRow.LOCATION, fixes.onlyInSettings),
        account = account,
        version = version,
        theme = theme,
        onBack = onBack,
        // Only a location that stops a trip has the button.
        onFix = { fixes.fix(if (readiness.state == ReadinessState.LocationOff) PermissionRow.LOCATION_SERVICES else PermissionRow.LOCATION) },
        onPermissionCheck = onPermissionCheck,
        onSignOut = accountViewModel::onSignOut,
        onSignedOut = onSignedOut,
        onChooseTheme = { if (viewModel.onChooseTheme(it)) activity?.recreate() },
        onHelp = onHelp,
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
internal fun SettingsContent(
    readiness: Readiness,
    locationFix: Fix,
    account: AccountUiState,
    version: String,
    theme: ThemeChoice,
    onBack: () -> Unit,
    onFix: () -> Unit,
    onPermissionCheck: () -> Unit,
    onSignOut: () -> Unit,
    onSignedOut: () -> Unit,
    onChooseTheme: (ThemeChoice) -> Unit,
    onHelp: () -> Unit,
    onShare: () -> Unit,
) {
    // Not a tap: it fires once the phone has forgotten the driver.
    LaunchedEffect(account.signedOut) { if (account.signedOut) onSignedOut() }

    var confirming by rememberSaveable { mutableStateOf(false) }
    val scroll = rememberScrollState()
    val fold = remember(scroll) { HeaderFold(scroll) }

    Column(Modifier.fillMaxSize().nestedScroll(fold.settling).verticalScroll(scroll)) {
        // Over the rows, which go under it once it has folded.
        ReadinessHeader(readiness, locationFix, fold, onBack, onFix, onPermissionCheck, Modifier.zIndex(1f))
        Column(
            Modifier
                .clearOfHeader(fold)
                .windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal + WindowInsetsSides.Bottom))
                .padding(start = 16.dp, top = 30.dp, end = 16.dp, bottom = 32.dp),
        ) {
            AccountCard(account) { confirming = true }
            GroupTitle(R.string.settings_group_phone)
            PermissionCheckRow(readiness.state, onPermissionCheck)
            GroupTitle(R.string.settings_group_app)
            LanguageChooser { shown, opensPhoneSettings, onClick ->
                SettingsRow(
                    icon = R.drawable.ic_language,
                    title = stringResource(R.string.language_list_title),
                    detail = if (opensPhoneSettings) {
                        "${shown.ownName}\n${stringResource(R.string.language_row_phone_note)}"
                    } else {
                        shown.ownName
                    },
                    place = GroupPlace.FIRST,
                    opens = true,
                    onClick = onClick,
                )
            }
            ThemeRow(theme, GroupPlace.LAST, onChooseTheme)
            GroupTitle(R.string.help_section_about)
            SettingsRow(R.drawable.ic_help, stringResource(R.string.help_title), null, GroupPlace.FIRST, opens = true, onClick = onHelp)
            SettingsRow(R.drawable.ic_share, stringResource(R.string.help_share), stringResource(R.string.help_share_detail), GroupPlace.MIDDLE, onClick = onShare)
            SettingsRow(R.drawable.ic_info, stringResource(R.string.help_version), isolated(version), GroupPlace.LAST)
        }
    }

    if (confirming) SignOutDialog(account.signingOut, onSignOut, onDismiss = { confirming = false })
}

/**
 * Who is signed in, and to which server, beside Sign out: phones go from driver to driver. An email too long for its line
 * breaks after its @ or before a dot.
 */
@Composable
private fun AccountCard(account: AccountUiState, onSignOut: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val server = account.server?.let { stringResource(R.string.preflight_row_state, stringResource(R.string.help_server), isolated(it)) }
    val said = listOfNotNull(account.email?.let { isolated(it) }, server).joinToString()
    val words: @Composable (Modifier) -> Unit = { modifier ->
        Column(modifier.clearAndSetSemantics { contentDescription = said }) {
            account.email?.let { Text(isolated(breakable(it)), style = MaterialTheme.typography.titleMedium) }
            account.server?.let {
                Text(isolated(breakable(it)), style = MaterialTheme.typography.bodyMedium, color = colors.onSurfaceVariant)
            }
        }
    }
    val button: @Composable () -> Unit = {
        Button(
            onClick = onSignOut,
            colors = ButtonDefaults.buttonColors(containerColor = colors.surface, contentColor = colors.onSurface),
            modifier = Modifier.heightIn(min = 48.dp),
        ) {
            Text(stringResource(R.string.account_sign_out))
        }
    }
    val card = Modifier.fillMaxWidth().background(colors.surfaceContainer, AccountShape).padding(start = 20.dp, top = 14.dp, end = 12.dp, bottom = 14.dp)
    val stacks = LocalDensity.current.fontScale >= STACKS_FROM_TEXT || isNarrow()
    if (stacks) {
        Column(card, verticalArrangement = Arrangement.spacedBy(10.dp)) {
            words(Modifier)
            button()
        }
    } else {
        Row(card, horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
            words(Modifier.weight(1f))
            button()
        }
    }
}

private const val MAY_BREAK_HERE = '\u200B'

internal fun breakable(address: String): String = address.replace("@", "@$MAY_BREAK_HERE").replace(".", "$MAY_BREAK_HERE.")

@Composable
private fun PermissionCheckRow(state: ReadinessState, onClick: () -> Unit) {
    val allAllowed = state is ReadinessState.Ready && state.toFix.isEmpty()
    SettingsRow(
        icon = if (allAllowed) R.drawable.ic_verified_user else R.drawable.ic_gpp_maybe,
        title = stringResource(R.string.permission_check_title),
        detail = when (state) {
            ReadinessState.LocationRefused -> stringResource(R.string.settings_permissions_refused)
            ReadinessState.LocationOff -> stringResource(R.string.settings_permissions_off)
            is ReadinessState.Ready ->
                if (allAllowed) {
                    stringResource(R.string.settings_permissions_all)
                } else {
                    pluralStringResource(R.plurals.settings_fix_many, state.toFix.size, state.toFix.size)
                }
        },
        place = GroupPlace.ONLY,
        opens = true,
        iconTint = if (allAllowed) Color.Unspecified else AppTheme.problemColors.iconOnSurface,
        onClick = onClick,
    )
}

/** A row of a group: its icon, its title over its value, and an arrow when it [opens] something in the app. */
@Composable
internal fun SettingsRow(
    @DrawableRes icon: Int,
    title: String,
    detail: String?,
    place: GroupPlace,
    opens: Boolean = false,
    iconTint: Color = Color.Unspecified,
    onClick: (() -> Unit)? = null,
) {
    ListItem(
        headlineContent = { Text(title) },
        supportingContent = detail?.let { { Text(it) } },
        leadingContent = { Icon(painterResource(icon), contentDescription = null, tint = iconTint.takeOrElse { LocalContentColor.current }) },
        trailingContent = if (opens) {
            { Icon(painterResource(R.drawable.ic_chevron_right), contentDescription = null) }
        } else {
            null
        },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.groupRow(place).then(if (onClick != null) Modifier.clickable(role = Role.Button, onClick = onClick) else Modifier),
    )
}
