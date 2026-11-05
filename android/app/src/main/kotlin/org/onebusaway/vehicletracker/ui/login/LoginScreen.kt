package org.onebusaway.vehicletracker.ui.login

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.input.KeyboardActionHandler
import androidx.compose.foundation.text.input.TextFieldLineLimits
import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.foundation.text.input.TextObfuscationMode
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedSecureTextField
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.ContentType
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.colorResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentType
import androidx.compose.ui.semantics.error
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.merge
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.AttentionBanner
import org.onebusaway.vehicletracker.ui.routes.RouteBadge
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.AppTheme

private val LogoShape = RoundedCornerShape(14.dp)
private val TripStillOpenShape = RoundedCornerShape(20.dp)
private val LogoSize = 52.dp

// An adaptive icon's foreground is drawn on 108 dp, of which a launcher shows the middle 72.
private const val ICON_FOREGROUND_SCALE = 108f / 72f

@Composable
fun LoginScreen(
    onLoginSuccess: () -> Unit,
    viewModel: LoginViewModel = hiltViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    LoginScreenContent(
        state = state,
        server = viewModel.server,
        email = viewModel.email,
        password = viewModel.password,
        onChangeServer = viewModel::onChangeServer,
        onEdited = viewModel::onEdited,
        onSignIn = { viewModel.onSignIn(onLoginSuccess) },
    )
}

/**
 * The app's icon, a title and three fields, each saying under it what is wrong with it, then the button. The whole
 * form scrolls as one, so the keyboard pushes it up rather than hiding the button, and nothing on it jumps.
 *
 * Signing in [LoginUiState.again], during a trip, the trip that is still open takes the icon's place and only the
 * password is left to type.
 */
@Composable
fun LoginScreenContent(
    state: LoginUiState,
    server: TextFieldState,
    email: TextFieldState,
    password: TextFieldState,
    onChangeServer: () -> Unit,
    onEdited: () -> Unit,
    onSignIn: () -> Unit,
) {
    val focus = remember { LoginField.entries.associateWith { FocusRequester() } }
    val error = state.error
    LaunchedEffect(error) {
        if (error is LoginError.MissingField) focus.getValue(error.field).requestFocus()
    }
    LaunchedEffect(state.again) {
        if (state.again) focus.getValue(LoginField.PASSWORD).requestFocus()
    }
    // Change opens the field to type in; an unreachable server opens it too, but only to show the error under it.
    LaunchedEffect(state.changingServer) {
        if (state.changingServer && error !is LoginError.Unreachable) focus.getValue(LoginField.SERVER).requestFocus()
    }
    val latestOnEdited by rememberUpdatedState(onEdited)
    LaunchedEffect(server, email, password) {
        merge(*listOf(server, email, password).map { field -> snapshotFlow { field.text.toString() }.drop(1) }.toTypedArray())
            .collect { latestOnEdited() }
    }
    val buttonInView = remember { BringIntoViewRequester() }
    var passwordFocused by remember { mutableStateOf(false) }
    val ime = WindowInsets.ime
    val density = LocalDensity.current
    // The scroll keeps the focused field above the keyboard by itself; this keeps the button under Password there too,
    // following the keyboard as it slides up.
    LaunchedEffect(passwordFocused) {
        if (passwordFocused) snapshotFlow { ime.getBottom(density) }.collectLatest { buttonInView.bringIntoView() }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(start = 24.dp, top = 56.dp, end = 24.dp, bottom = 24.dp),
    ) {
        if (!state.again) {
            AppIcon()
            Spacer(Modifier.height(24.dp))
        } else if (state.openTrip != null) {
            TripStillOpen(state.openTrip)
            Spacer(Modifier.height(28.dp))
        }
        Text(
            text = stringResource(if (state.again) R.string.login_again_title else R.string.login_title),
            style = MaterialTheme.typography.displaySmall,
            modifier = Modifier.semantics { heading() },
        )
        Spacer(Modifier.height(8.dp))
        Text(
            text = if (state.again && state.savedEmail != null) {
                stringResource(R.string.login_again_subtitle, state.savedEmail)
            } else {
                stringResource(R.string.login_subtitle)
            },
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(32.dp))
        // An error line under a field slides what is below it down, rather than snapping it.
        Column(
            modifier = Modifier.animateContentSize(AppMotion.defaultSpatialSpec()),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            val enabled = !state.loading
            if (state.showsServerField) {
                LoginTextField(
                    state = server,
                    label = stringResource(R.string.login_server_url_label),
                    error = fieldErrorRes(error, LoginField.SERVER)?.let { stringResource(it) },
                    enabled = enabled,
                    keyboardOptions = KeyboardOptions(
                        keyboardType = KeyboardType.Uri,
                        autoCorrectEnabled = false,
                        imeAction = ImeAction.Next,
                    ),
                    modifier = Modifier.focusRequester(focus.getValue(LoginField.SERVER)),
                )
            } else if (state.savedServer != null) {
                // During a trip the server is the trip's, so it is not for changing here.
                SavedServer(state.savedServer, enabled, onChange = if (state.again) null else onChangeServer)
            }
            LoginTextField(
                state = email,
                label = stringResource(R.string.login_email_label),
                error = fieldErrorRes(error, LoginField.EMAIL)?.let { stringResource(it) },
                enabled = enabled,
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Email,
                    autoCorrectEnabled = false,
                    imeAction = ImeAction.Next,
                ),
                modifier = Modifier
                    .focusRequester(focus.getValue(LoginField.EMAIL))
                    .semantics { contentType = ContentType.EmailAddress + ContentType.Username },
            )
            PasswordField(
                state = password,
                error = fieldErrorRes(error, LoginField.PASSWORD)?.let { stringResource(it) },
                enabled = enabled,
                onSignIn = onSignIn,
                modifier = Modifier
                    .focusRequester(focus.getValue(LoginField.PASSWORD))
                    .onFocusChanged { passwordFocused = it.isFocused },
            )
            bannerOf(error)?.let { banner ->
                val unreachable = (error as? LoginError.Unreachable)?.server
                AttentionBanner(
                    icon = banner.icon,
                    text = if (unreachable != null) stringResource(banner.text, serverLabel(unreachable)) else stringResource(banner.text),
                )
            }
            Spacer(Modifier.height(8.dp))
        }
        SignInButton(
            label = stringResource(if (state.again) R.string.login_again_button else R.string.login_button),
            loading = state.loading,
            onSignIn = onSignIn,
            modifier = Modifier.bringIntoViewRequester(buttonInView),
        )
    }
}

/** The launcher icon, drawn as a launcher draws it, so the sign-in screen changes with it. */
@Composable
private fun AppIcon() {
    Box(
        modifier = Modifier
            .size(LogoSize)
            .clip(LogoShape)
            .background(colorResource(R.color.ic_launcher_background)),
        contentAlignment = Alignment.Center,
    ) {
        // The title next to it says what the screen is; the icon only adds the brand.
        Image(
            painter = painterResource(R.drawable.ic_launcher_foreground),
            contentDescription = null,
            modifier = Modifier.requiredSize(LogoSize * ICON_FOREGROUND_SCALE),
        )
    }
}

/** The trip that was reporting when the session ended, which signing in again picks up. */
@Composable
private fun TripStillOpen(route: RouteBadgeModel) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(AppTheme.problemColors.attentionContainer, TripStillOpenShape)
            .padding(16.dp)
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        // The message names the route, so the badge is not read out as well.
        RouteBadge(
            route = route,
            modifier = Modifier.sizeIn(minWidth = 44.dp, minHeight = 44.dp).clearAndSetSemantics {},
            style = MaterialTheme.typography.titleLarge,
            shape = RoundedCornerShape(10.dp),
        )
        Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(stringResource(R.string.login_trip_open_title), style = MaterialTheme.typography.titleMedium)
            Text(
                text = stringResource(R.string.login_trip_open_message, route.shortName),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * The server the phone signed in to last, as one quiet line: most drivers never need to change it. [onChange] is null
 * where it is not for changing.
 */
@Composable
private fun SavedServer(server: String, enabled: Boolean, onChange: (() -> Unit)?) {
    Row(
        modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            painter = painterResource(R.drawable.ic_dns),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(20.dp),
        )
        Text(
            text = stringResource(R.string.login_server),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = serverLabel(server),
            style = MaterialTheme.typography.titleSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (onChange != null) {
            TextButton(onClick = onChange, enabled = enabled) {
                Text(stringResource(R.string.login_change_server))
            }
        }
    }
}

@Composable
private fun LoginTextField(
    state: TextFieldState,
    label: String,
    error: String?,
    enabled: Boolean,
    keyboardOptions: KeyboardOptions,
    modifier: Modifier,
) {
    OutlinedTextField(
        state = state,
        label = { Text(label) },
        enabled = enabled,
        isError = error != null,
        supportingText = error?.let { { FieldError(it) } },
        keyboardOptions = keyboardOptions,
        lineLimits = TextFieldLineLimits.SingleLine,
        modifier = modifier.fillMaxWidth().fieldError(error),
    )
}

@Composable
private fun PasswordField(
    state: TextFieldState,
    error: String?,
    enabled: Boolean,
    onSignIn: () -> Unit,
    modifier: Modifier,
) {
    var shown by rememberSaveable { mutableStateOf(false) }
    OutlinedSecureTextField(
        state = state,
        label = { Text(stringResource(R.string.login_password_label)) },
        enabled = enabled,
        isError = error != null,
        supportingText = error?.let { { FieldError(it) } },
        textObfuscationMode = if (shown) TextObfuscationMode.Visible else TextObfuscationMode.RevealLastTyped,
        trailingIcon = {
            IconButton(onClick = { shown = !shown }, enabled = enabled) {
                Icon(
                    painter = painterResource(if (shown) R.drawable.ic_visibility_off else R.drawable.ic_visibility),
                    contentDescription = stringResource(if (shown) R.string.login_hide_password else R.string.login_show_password),
                )
            }
        },
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.Password,
            autoCorrectEnabled = false,
            imeAction = ImeAction.Done,
        ),
        onKeyboardAction = KeyboardActionHandler { hideKeyboard ->
            hideKeyboard()
            onSignIn()
        },
        modifier = modifier.fillMaxWidth().fieldError(error),
    )
}

/**
 * TalkBack reads a field's error as its own, in place of the "Invalid input" the field says by itself; the line
 * under the field is then left out, so the error is not read twice.
 */
private fun Modifier.fieldError(message: String?): Modifier =
    if (message == null) this else semantics { error(message) }

@Composable
private fun FieldError(message: String) {
    Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(painterResource(R.drawable.ic_error), contentDescription = null, modifier = Modifier.size(18.dp))
        Text(message, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.clearAndSetSemantics {})
    }
}

/** Full width, so "Signing in" and its spinner change only what is inside it, never its size. */
@Composable
private fun SignInButton(label: String, loading: Boolean, onSignIn: () -> Unit, modifier: Modifier) {
    Button(
        onClick = onSignIn,
        enabled = !loading,
        // Disabled only while signing in, and then it keeps its colour, with the spinner in it.
        colors = ButtonDefaults.buttonColors(
            disabledContainerColor = MaterialTheme.colorScheme.primary,
            disabledContentColor = MaterialTheme.colorScheme.onPrimary,
        ),
        modifier = modifier
            .fillMaxWidth()
            .height(56.dp)
            .semantics { liveRegion = LiveRegionMode.Polite },
    ) {
        if (loading) {
            CircularProgressIndicator(
                color = LocalContentColor.current,
                strokeWidth = 2.dp,
                modifier = Modifier.size(20.dp).clearAndSetSemantics {},
            )
            Spacer(Modifier.width(10.dp))
        }
        Text(
            text = if (loading) stringResource(R.string.login_signing_in) else label,
            style = MaterialTheme.typography.titleMedium,
        )
    }
}

/** The error a field shows under it, if this one is about that field. */
@StringRes
internal fun fieldErrorRes(error: LoginError?, field: LoginField): Int? = when {
    error is LoginError.MissingField && error.field == field -> when (field) {
        LoginField.SERVER -> R.string.login_error_missing_server
        LoginField.EMAIL -> R.string.login_error_missing_email
        LoginField.PASSWORD -> R.string.login_error_missing_password
    }
    error is LoginError.InvalidCredentials && field == LoginField.PASSWORD -> R.string.login_error_invalid_credentials
    error is LoginError.Unreachable && field == LoginField.SERVER -> R.string.login_error_check_address
    else -> null
}

/** An error that is about no one field, shown above the button. [text] may name the server, as its one argument. */
internal data class LoginBanner(@DrawableRes val icon: Int, @StringRes val text: Int)

internal fun bannerOf(error: LoginError?): LoginBanner? = when (error) {
    is LoginError.Unreachable -> LoginBanner(R.drawable.ic_cloud_off, R.string.login_error_unreachable)
    LoginError.ServerError -> LoginBanner(R.drawable.ic_error, R.string.login_error_server)
    else -> null
}
