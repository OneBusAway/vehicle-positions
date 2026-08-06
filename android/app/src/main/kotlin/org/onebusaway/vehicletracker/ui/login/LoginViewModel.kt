package org.onebusaway.vehicletracker.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.SessionStore
import javax.inject.Inject

data class LoginUiState(
    val serverUrl: String = "",
    val email: String = "",
    val password: String = "",
    val loading: Boolean = false,
    val error: LoginError? = null,
)

enum class LoginError { INVALID_CREDENTIALS, NETWORK, OTHER }

@HiltViewModel
class LoginViewModel @Inject constructor(
    private val authRepository: AuthRepository,
    sessionStore: SessionStore,
) : ViewModel() {
    private val _uiState = MutableStateFlow(LoginUiState())
    val uiState: StateFlow<LoginUiState> = _uiState.asStateFlow()

    init {
        // Prefill the server URL field from the last successful login, without clobbering
        // anything the user has already typed (guards against a late/second emission).
        sessionStore.session
            .onEach { session ->
                val savedUrl = session.serverUrl
                if (!savedUrl.isNullOrBlank()) {
                    _uiState.update { if (it.serverUrl.isBlank()) it.copy(serverUrl = savedUrl) else it }
                }
            }
            .launchIn(viewModelScope)
    }

    fun onServerUrlChange(value: String) = _uiState.update { it.copy(serverUrl = value, error = null) }
    fun onEmailChange(value: String) = _uiState.update { it.copy(email = value, error = null) }
    fun onPasswordChange(value: String) = _uiState.update { it.copy(password = value, error = null) }

    fun onLogin(onSuccess: () -> Unit) {
        val state = _uiState.value
        if (state.serverUrl.isBlank() || state.email.isBlank() || state.password.isBlank()) {
            _uiState.update { it.copy(error = LoginError.OTHER) }
            return
        }
        _uiState.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            val result = authRepository.login(state.serverUrl, state.email, state.password)
            result.fold(
                onSuccess = {
                    _uiState.update { it.copy(loading = false, error = null) }
                    onSuccess()
                },
                onFailure = { error ->
                    val mapped = when (error) {
                        is ApiError.Unauthorized -> LoginError.INVALID_CREDENTIALS
                        is ApiError.Other -> if (error.msg == "network") LoginError.NETWORK else LoginError.OTHER
                        else -> LoginError.OTHER
                    }
                    _uiState.update { it.copy(loading = false, error = mapped) }
                },
            )
        }
    }
}
