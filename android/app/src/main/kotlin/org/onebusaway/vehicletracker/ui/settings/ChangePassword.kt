package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.foundation.text.input.TextFieldState
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.PasswordChangeFailure
import org.onebusaway.vehicletracker.data.PasswordChanger
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.ui.login.serverLabel
import javax.inject.Inject

/** The server's rules (`minPasswordLength`, `maxPasswordBytes`): at least 8 characters, at most 72 bytes. */
internal const val MIN_PASSWORD_LENGTH = 8
internal const val MAX_PASSWORD_BYTES = 72

/** Change password's fields, in order. */
enum class PasswordField { CURRENT, NEW, AGAIN }

enum class NewPasswordProblem { TOO_SHORT, TOO_LONG }

/**
 * What is wrong with [new] by the server's rules: characters counted as it counts them, by code point, and bytes of
 * UTF-8, bcrypt's limit.
 */
internal fun problemWith(new: String): NewPasswordProblem? = when {
    new.codePointCount(0, new.length) < MIN_PASSWORD_LENGTH -> NewPasswordProblem.TOO_SHORT
    new.toByteArray(Charsets.UTF_8).size > MAX_PASSWORD_BYTES -> NewPasswordProblem.TOO_LONG
    else -> null
}

/** Change password can be tapped once all three hold something. */
internal fun canChange(current: CharSequence, new: CharSequence, again: CharSequence): Boolean =
    current.isNotEmpty() && new.isNotEmpty() && again.isNotEmpty()

/**
 * What Change password decides apart from the screen: the line under each field, and the [banner] over them for a
 * failure no field is to blame for. While [changing], the change is with the server.
 */
data class PasswordChangeForm(
    val newProblem: NewPasswordProblem? = null,
    val noMatch: Boolean = false,
    val wrongCurrent: Boolean = false,
    val banner: PasswordChangeFailure? = null,
    val changing: Boolean = false,
) {
    /** Leaving New password says at once what is wrong with it. Left empty, nothing is wrong yet. */
    fun leftNew(new: String): PasswordChangeForm = if (new.isEmpty()) this else copy(newProblem = problemWith(new))

    /**
     * What typed checks to, changing if it can go. Null while a change is out, or before all three hold something,
     * when the button does nothing.
     */
    fun begin(current: String, new: String, again: String): PasswordChangeForm? {
        if (changing || !canChange(current, new, again)) return null
        val checked = copy(newProblem = problemWith(new), noMatch = new != again, wrongCurrent = false, banner = null)
        return if (checked.newProblem == null && !checked.noMatch) checked.copy(changing = true) else checked
    }

    fun failed(failure: PasswordChangeFailure): PasswordChangeForm = copy(
        changing = false,
        wrongCurrent = failure == PasswordChangeFailure.WRONG_CURRENT,
        banner = failure.takeIf { it != PasswordChangeFailure.WRONG_CURRENT },
    )

    /** A line under a field is about what was typed there, so it goes once that field is typed in again; the banner goes with any edit. */
    fun edited(field: PasswordField): PasswordChangeForm = when (field) {
        PasswordField.CURRENT -> copy(banner = null, wrongCurrent = false)
        PasswordField.NEW -> copy(banner = null, newProblem = null, noMatch = false)
        PasswordField.AGAIN -> copy(banner = null, noMatch = false)
    }
}

/** How Change password ends: the password changed, or the sign-in turned out to be over. */
enum class ChangePasswordOutcome { CHANGED, SESSION_ENDED }

@HiltViewModel
class ChangePasswordViewModel @Inject constructor(
    private val passwords: PasswordChanger,
    private val auth: AuthRepository,
    sessionStore: SessionStore,
) : ViewModel() {
    val current = TextFieldState()
    val new = TextFieldState()
    val again = TextFieldState()

    private val _form = MutableStateFlow(PasswordChangeForm())
    val form: StateFlow<PasswordChangeForm> = _form.asStateFlow()

    /** The server signed in, as the banner names it. */
    val server: StateFlow<String?> = sessionStore.session.map { it.serverUrl?.let(::serverLabel) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    private val _outcome = MutableStateFlow<ChangePasswordOutcome?>(null)
    val outcome: StateFlow<ChangePasswordOutcome?> = _outcome.asStateFlow()

    fun onEdited(field: PasswordField) = _form.update { it.edited(field) }

    fun onLeftNew() = _form.update { it.leftNew(new.text.toString()) }

    fun onChange() {
        val current = current.text.toString()
        val new = new.text.toString()
        val next = _form.value.begin(current, new, again.text.toString()) ?: return
        _form.value = next
        if (!next.changing) return
        viewModelScope.launch {
            when (val failure = passwords.change(current, new)) {
                null -> _outcome.value = ChangePasswordOutcome.CHANGED
                // As any screen does on a 401: Sign in, with the server and the email kept.
                PasswordChangeFailure.SESSION_OVER -> {
                    auth.endSessionLocally()
                    _outcome.value = ChangePasswordOutcome.SESSION_ENDED
                }
                else -> _form.update { it.failed(failure) }
            }
        }
    }
}
