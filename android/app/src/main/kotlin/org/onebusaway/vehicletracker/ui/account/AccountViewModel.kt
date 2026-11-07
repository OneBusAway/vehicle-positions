package org.onebusaway.vehicletracker.ui.account

import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.ui.login.serverLabel
import javax.inject.Inject

private const val TAG = "AccountViewModel"

/**
 * How long Sign out waits on the server, for the pending ends and the logout together. Out of coverage each call
 * could take OkHttp's 20 s on its own; the phone signs out anyway, and the server's session then ends with its tokens.
 */
internal const val SIGN_OUT_WAIT_MS = 10_000L

data class AccountUiState(
    val email: String? = null,
    /** The server as the driver knows it, [serverLabel]; null before it is read. */
    val server: String? = null,
    val signingOut: Boolean = false,
    val signedOut: Boolean = false,
)

/** Who is signed in, to which server, and Sign out, for the account menu on each picker step. */
@HiltViewModel
class AccountViewModel @Inject constructor(
    sessionStore: SessionStore,
    private val authRepository: AuthRepository,
    private val tripRepository: TripRepository,
) : ViewModel() {
    private val signingOut = MutableStateFlow(false)
    private val signedOut = MutableStateFlow(false)

    val uiState: StateFlow<AccountUiState> = combine(sessionStore.session, signingOut, signedOut) { session, busy, done ->
        AccountUiState(session.email, session.serverUrl?.let(::serverLabel), signingOut = busy, signedOut = done)
    }.stateIn(viewModelScope, SharingStarted.Eagerly, AccountUiState())

    /**
     * Sends this driver's pending ends while the token still works, logs out on the server, then forgets the driver.
     * The server's part is best effort: a driver handing the phone on is never left signed in, so a failure, or no
     * answer within [SIGN_OUT_WAIT_MS], is logged and the sign-out goes on. An end that fails waits for this driver's
     * next start.
     */
    fun onSignOut() {
        // The dialog's button is disabled while signing out, but a second tap already on its way must not log out
        // twice.
        if (!signingOut.compareAndSet(expect = false, update = true)) return
        viewModelScope.launch {
            try {
                val answered = withTimeoutOrNull(SIGN_OUT_WAIT_MS) {
                    tripRepository.sendUnconfirmedEnds().onFailure {
                        Log.w(TAG, "Could not send the trips ended on this phone; they wait for this driver's next start", it)
                    }
                    authRepository.logout().onFailure {
                        Log.w(TAG, "Could not log out on the server; the session there ends when its tokens expire", it)
                    }
                }
                if (answered == null) {
                    Log.w(TAG, "The server took longer than $SIGN_OUT_WAIT_MS ms; signing out on this phone only")
                }
            } finally {
                // The driver asked to be signed out, so the phone forgets them even if this screen goes first.
                withContext(NonCancellable) { authRepository.forgetDriver() }
            }
            signedOut.value = true
        }
    }
}
