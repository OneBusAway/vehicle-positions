package org.onebusaway.vehicletracker.ui.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.input.KeyboardActionHandler
import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.foundation.text.input.TextObfuscationMode
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedSecureTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.ContentType
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentType
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.merge
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.PasswordChangeFailure
import org.onebusaway.vehicletracker.ui.components.AttentionBanner
import org.onebusaway.vehicletracker.ui.isolated
import org.onebusaway.vehicletracker.ui.login.FieldError
import org.onebusaway.vehicletracker.ui.login.fieldError

/**
 * Change password (the design's sheet 09): the current password, the new one twice, and the button at the foot, over
 * the keyboard. A line under a field says what is wrong with it, and a banner on top what no field is to blame for.
 * While the change is out, the fields and Back wait.
 */
@Composable
fun ChangePasswordScreen(
    onBack: () -> Unit,
    onChanged: () -> Unit,
    onSessionEnded: () -> Unit,
    viewModel: ChangePasswordViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()
    val server by viewModel.server.collectAsStateWithLifecycle()
    val outcome by viewModel.outcome.collectAsStateWithLifecycle()
    // Not a tap: it fires once the server has answered.
    LaunchedEffect(outcome) {
        when (outcome) {
            ChangePasswordOutcome.CHANGED -> onChanged()
            ChangePasswordOutcome.SESSION_ENDED -> onSessionEnded()
            null -> Unit
        }
    }
    ChangePasswordContent(
        form = form,
        server = server,
        current = viewModel.current,
        new = viewModel.new,
        again = viewModel.again,
        onBack = onBack,
        onChange = viewModel::onChange,
        onEdited = viewModel::onEdited,
        onLeftNew = viewModel::onLeftNew,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ChangePasswordContent(
    form: PasswordChangeForm,
    server: String?,
    current: TextFieldState,
    new: TextFieldState,
    again: TextFieldState,
    onBack: () -> Unit,
    onChange: () -> Unit,
    onEdited: (PasswordField) -> Unit,
    onLeftNew: () -> Unit,
) {
    val latestOnEdited by rememberUpdatedState(onEdited)
    LaunchedEffect(current, new, again) {
        merge(
            snapshotFlow { current.text.toString() }.drop(1).map { PasswordField.CURRENT },
            snapshotFlow { new.text.toString() }.drop(1).map { PasswordField.NEW },
            snapshotFlow { again.text.toString() }.drop(1).map { PasswordField.AGAIN },
        ).collect { latestOnEdited(it) }
    }
    // Back waits while the change is out, as the fields do.
    BackHandler(enabled = form.changing) {}
    val enabled = !form.changing

    Column(Modifier.fillMaxSize().imePadding()) {
        TopAppBar(
            title = { Text(stringResource(R.string.change_password_title), modifier = Modifier.semantics { heading() }) },
            navigationIcon = {
                IconButton(onClick = onBack, enabled = enabled) {
                    Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
                }
            },
        )
        Column(
            Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            form.banner?.let { failure ->
                val (icon, text) = bannerOf(failure, server)
                AttentionBanner(icon = icon, text = text, modifier = Modifier.padding(bottom = 8.dp))
            }
            PasswordInput(
                state = current,
                label = stringResource(R.string.change_password_current),
                error = if (form.wrongCurrent) stringResource(R.string.change_password_wrong_current) else null,
                enabled = enabled,
                contentType = ContentType.Password,
                imeAction = ImeAction.Next,
            )
            PasswordInput(
                state = new,
                label = stringResource(R.string.change_password_new),
                error = form.newProblem?.let {
                    stringResource(if (it == NewPasswordProblem.TOO_SHORT) R.string.change_password_too_short else R.string.change_password_too_long)
                },
                note = stringResource(R.string.change_password_rule),
                enabled = enabled,
                contentType = ContentType.NewPassword,
                imeAction = ImeAction.Next,
                onLeft = onLeftNew,
            )
            PasswordInput(
                state = again,
                label = stringResource(R.string.change_password_again),
                error = if (form.noMatch) stringResource(R.string.change_password_no_match) else null,
                enabled = enabled,
                contentType = ContentType.NewPassword,
                imeAction = ImeAction.Done,
                onDone = onChange,
            )
            Text(
                text = stringResource(R.string.change_password_others),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(start = 16.dp, top = 8.dp, bottom = 16.dp),
            )
        }
        ChangeButton(
            changing = form.changing,
            canChange = canChange(current.text, new.text, again.text),
            onChange = onChange,
            modifier = Modifier.padding(start = 16.dp, top = 8.dp, end = 16.dp, bottom = 16.dp),
        )
    }
}

/** The banner for [failure]: its icon and words, naming the server it could not reach. */
@Composable
private fun bannerOf(failure: PasswordChangeFailure, server: String?): Pair<Int, String> = when (failure) {
    PasswordChangeFailure.UNREACHABLE -> R.drawable.ic_cloud_off to
        if (server != null) stringResource(R.string.change_password_unreachable, isolated(server)) else stringResource(R.string.change_password_unreachable_server)
    PasswordChangeFailure.TOO_MANY_TRIES -> R.drawable.ic_error to stringResource(R.string.login_error_too_many_tries)
    PasswordChangeFailure.OLDER_SERVER -> R.drawable.ic_dns to stringResource(R.string.change_password_older_server)
    else -> R.drawable.ic_error to stringResource(R.string.change_password_failed)
}

/** A password field with the eye at its end. [note] goes under it while it has no [error] to show there. */
@Composable
private fun PasswordInput(
    state: TextFieldState,
    label: String,
    error: String?,
    enabled: Boolean,
    contentType: ContentType,
    imeAction: ImeAction,
    note: String? = null,
    onLeft: () -> Unit = {},
    onDone: () -> Unit = {},
) {
    var shown by rememberSaveable { mutableStateOf(false) }
    var focused by rememberSaveable { mutableStateOf(false) }
    OutlinedSecureTextField(
        state = state,
        label = { Text(label) },
        enabled = enabled,
        isError = error != null,
        supportingText = when {
            error != null -> { { FieldError(error) } }
            note != null -> { { Text(note) } }
            else -> null
        },
        textObfuscationMode = if (shown) TextObfuscationMode.Visible else TextObfuscationMode.RevealLastTyped,
        trailingIcon = {
            IconButton(onClick = { shown = !shown }, enabled = enabled) {
                Icon(
                    painter = painterResource(if (shown) R.drawable.ic_visibility_off else R.drawable.ic_visibility),
                    contentDescription = stringResource(if (shown) R.string.login_hide_password else R.string.login_show_password),
                )
            }
        },
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, autoCorrectEnabled = false, imeAction = imeAction),
        onKeyboardAction = if (imeAction == ImeAction.Done) {
            KeyboardActionHandler { hideKeyboard ->
                hideKeyboard()
                onDone()
            }
        } else {
            null
        },
        modifier = Modifier
            .fillMaxWidth()
            .onFocusChanged {
                if (focused && !it.isFocused) onLeft()
                focused = it.isFocused
            }
            .semantics { this.contentType = contentType }
            .fieldError(error),
    )
}

/** Full width, so "Changing password" and its spinner change only what is inside it, never its size. */
@Composable
private fun ChangeButton(changing: Boolean, canChange: Boolean, onChange: () -> Unit, modifier: Modifier) {
    Button(
        onClick = onChange,
        enabled = canChange && !changing,
        // While changing it keeps its colour, with the spinner in it; before all three hold something it is grey.
        colors = if (changing) {
            ButtonDefaults.buttonColors(
                disabledContainerColor = MaterialTheme.colorScheme.primary,
                disabledContentColor = MaterialTheme.colorScheme.onPrimary,
            )
        } else {
            ButtonDefaults.buttonColors()
        },
        modifier = modifier.fillMaxWidth().height(56.dp).semantics { liveRegion = LiveRegionMode.Polite },
    ) {
        if (changing) {
            CircularProgressIndicator(
                color = LocalContentColor.current,
                strokeWidth = 2.dp,
                modifier = Modifier.size(20.dp).clearAndSetSemantics {},
            )
            Spacer(Modifier.width(10.dp))
        }
        Text(
            text = stringResource(if (changing) R.string.change_password_changing else R.string.change_password_title),
            style = MaterialTheme.typography.titleMedium,
        )
    }
}
