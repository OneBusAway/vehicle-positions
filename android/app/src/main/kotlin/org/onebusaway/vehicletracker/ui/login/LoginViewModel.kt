package org.onebusaway.vehicletracker.ui.login

import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.ManagedServer
import org.onebusaway.vehicletracker.data.ManagedServerSource
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripGeometryStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.loadFor
import org.onebusaway.vehicletracker.di.DefaultServerUrl
import org.onebusaway.vehicletracker.ui.ARG_SIGN_IN_AGAIN
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import javax.inject.Inject

enum class LoginField { SERVER, EMAIL, PASSWORD }

/** Why a sign-in did not go through, each shown under the field it is about or, with no field to blame, above the button. */
sealed interface LoginError {
    data class MissingField(val field: LoginField) : LoginError
    data object InvalidCredentials : LoginError

    /** What was typed cannot be an http or https address, so nothing was asked. */
    data object InvalidAddress : LoginError

    /** The phone's network policy refused a plain http address, before anything was sent. */
    data object HttpsRequired : LoginError

    /** Nothing answered at [server], the address the sign-in went to. */
    data class Unreachable(val server: String) : LoginError

    /** Something answered at [server], but not as a tracker server does: a 404, or a web page. */
    data class NotATrackerServer(val server: String) : LoginError

    /** The server's limit on sign-ins. Nothing in the fields is wrong. */
    data object TooManyAttempts : LoginError

    /** The server answered, but not with a sign-in, such as a 500. */
    data object ServerError : LoginError
}

/** An error about the address, which opens the server field to sit under, and leaves the keyboard where it is. */
internal val LoginError.isAboutTheServer: Boolean
    get() = this is LoginError.InvalidAddress || this is LoginError.HttpsRequired ||
        this is LoginError.Unreachable || this is LoginError.NotATrackerServer

data class LoginUiState(
    /** Signing in again after the session ended during a trip, which is still open. */
    val again: Boolean = false,
    /** The open trip's route, once read off the phone; only when signing in [again]. */
    val openTrip: RouteBadgeModel? = null,
    /** The email this phone last signed in with; null before its first sign-in, or when an older build signed in. */
    val savedEmail: String? = null,
    /** The server this phone last signed in to; null before its first sign-in. */
    val savedServer: String? = null,
    /** The server the agency's device management sets, which the driver can't change. Never when signing in [again]. */
    val managedServer: String? = null,
    /** The agency's device management sets a server the app can't use, so it is ignored and the driver types one. */
    val managedServerInvalid: Boolean = false,
    val changingServer: Boolean = false,
    val loading: Boolean = false,
    val error: LoginError? = null,
) {
    /**
     * Once the phone knows its server it shows as one quiet line, until the driver asks to change it. The agency's
     * server is always a line.
     */
    val showsServerField: Boolean get() = managedServer == null && (savedServer == null || changingServer)
}

/**
 * The server as the driver knows it, "tracker.example.org", with its port when it is not the usual one; as it was
 * typed when it is not an http address.
 */
internal fun serverLabel(server: String): String {
    val url = server.toHttpUrlOrNull() ?: return server.trim()
    return if (url.port == HttpUrl.defaultPort(url.scheme)) url.host else "${url.host}:${url.port}"
}

/**
 * The address to sign in to, from what was typed: trimmed, and over https when no scheme was typed, since that is
 * how a driver is usually told the address. Null when it is not an http or https address at all.
 */
internal fun serverAddressOf(typed: String): String? {
    val trimmed = typed.trim()
    val withScheme = if ("://" in trimmed) trimmed else "https://$trimmed"
    return withScheme.toHttpUrlOrNull()?.toString()
}

/** [ARG_SIGN_IN_AGAIN] is set on the route Tracking opens once the session has ended; the first sign-in has none. */
@HiltViewModel
class LoginViewModel @Inject constructor(
    private val authRepository: AuthRepository,
    sessionStore: SessionStore,
    tripStateStore: TripStateStore,
    tripGeometryStore: TripGeometryStore,
    managedServerSource: ManagedServerSource,
    @param:DefaultServerUrl private val builtInServer: String?,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {
    val server = TextFieldState()
    val email = TextFieldState()
    val password = TextFieldState()

    private val _uiState = MutableStateFlow(LoginUiState(again = savedStateHandle[ARG_SIGN_IN_AGAIN] ?: false))
    val uiState: StateFlow<LoginUiState> = _uiState.asStateFlow()

    init {
        // Filled once, from the last sign-in or else the server built into the app, and only where the driver has not
        // started typing.
        viewModelScope.launch {
            val session = sessionStore.session.first()
            val saved = session.serverUrl?.takeIf { it.isNotBlank() }
            if (saved != null) {
                if (server.text.isEmpty()) server.setTextAndPlaceCursorAtEnd(saved)
                _uiState.update { it.copy(savedServer = saved) }
            } else if (builtInServer != null && !_uiState.value.again && server.text.isEmpty()) {
                // A field, not the saved line: the driver can still change it.
                server.setTextAndPlaceCursorAtEnd(builtInServer)
            }
            session.email?.takeIf { it.isNotBlank() }?.let { saved ->
                if (email.text.isEmpty()) email.setTextAndPlaceCursorAtEnd(saved)
                _uiState.update { it.copy(savedEmail = saved) }
            }
        }
        if (_uiState.value.again) {
            viewModelScope.launch {
                val trip = tripStateStore.activeTrip.first() ?: return@launch
                val route = RouteBadgeModel.forTrip(trip, tripGeometryStore.loadFor(trip))
                _uiState.update { it.copy(openTrip = route) }
            }
        } else {
            // Kept in step while Sign in shows. Not when signing in again: the open trip is on the saved server.
            viewModelScope.launch { managedServerSource.server.collect(::useManaged) }
        }
    }

    private fun useManaged(managed: ManagedServer) {
        val wasManaged = _uiState.value.managedServer != null
        when {
            managed.url != null -> server.setTextAndPlaceCursorAtEnd(managed.url)
            // The agency stopped setting it, and the driver couldn't type while it did: back to where Sign in starts.
            wasManaged -> server.setTextAndPlaceCursorAtEnd(_uiState.value.savedServer ?: builtInServer.orEmpty())
        }
        _uiState.update { it.copy(managedServer = managed.url, managedServerInvalid = managed.invalid) }
    }

    fun onChangeServer() = _uiState.update { it.copy(changingServer = true) }

    /** An error is about what was typed, so it goes once the driver types again. */
    fun onEdited() = _uiState.update { it.copy(error = null) }

    fun onSignIn(onSuccess: () -> Unit) {
        // The button is disabled while signing in, but a second tap already on its way must not sign in, and
        // navigate, twice.
        if (_uiState.value.loading) return
        val typedServer = server.text.toString()
        val missing = when {
            typedServer.isBlank() -> LoginField.SERVER
            email.text.isBlank() -> LoginField.EMAIL
            password.text.isBlank() -> LoginField.PASSWORD
            else -> null
        }
        if (missing != null) {
            _uiState.update { it.copy(error = LoginError.MissingField(missing)) }
            return
        }
        val serverUrl = serverAddressOf(typedServer)
        if (serverUrl == null) {
            failed(LoginError.InvalidAddress)
            return
        }
        _uiState.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            authRepository.login(serverUrl, email.text.toString(), password.text.toString()).fold(
                onSuccess = {
                    _uiState.update { it.copy(loading = false) }
                    onSuccess()
                },
                onFailure = { error ->
                    failed(
                        when {
                            error is ApiError.Unauthorized -> LoginError.InvalidCredentials
                            error is ApiError.CleartextBlocked -> LoginError.HttpsRequired
                            error is ApiError.NotATrackerServer -> LoginError.NotATrackerServer(serverUrl)
                            error is ApiError.TooManyAttempts -> LoginError.TooManyAttempts
                            error is ApiError.Other && error.msg == "network" -> LoginError.Unreachable(serverUrl)
                            else -> LoginError.ServerError
                        },
                    )
                },
            )
        }
    }

    // The server field is opened for an error about the server, so the error has a field to sit under. Not when
    // signing in again, nor when the agency sets the server: it isn't for changing, so the banner alone names it.
    private fun failed(error: LoginError) = _uiState.update {
        it.copy(
            loading = false,
            error = error,
            changingServer = it.changingServer || (error.isAboutTheServer && !it.again && it.managedServer == null),
        )
    }
}
