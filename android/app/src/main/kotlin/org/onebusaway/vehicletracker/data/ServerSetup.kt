package org.onebusaway.vehicletracker.data

import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ServerSetupLink.Decision
import org.onebusaway.vehicletracker.di.DebugBuild
import javax.inject.Inject
import javax.inject.Singleton

private const val LOG_TAG = "ServerSetup"

/**
 * Takes setup links from the system and holds the dialog each one leads to, which MainActivity shows over any screen.
 *
 * A link saves nothing. The driver agreeing puts its server in [linkedServer], in memory, for Sign in's field, and only
 * a sign-in saves an address, in the same write as the token it got there. An address saved on its own would sit next
 * to a token or a trip from another server, which would then go to the link's host with no password typed.
 *
 * The checks run in [scope], not the dialog's, so answering the dialog, which takes it off the screen, can't cancel
 * them.
 */
@Singleton
class ServerSetup @Inject constructor(
    private val sessionStore: SessionStore,
    private val tripStateStore: TripStateStore,
    private val managedServerSource: ManagedServerSource,
    @param:DebugBuild private val debuggable: Boolean,
    private val scope: CoroutineScope,
) {
    private val _prompt = MutableStateFlow<Decision?>(null)

    /** The dialog on screen, until the driver answers it. */
    val prompt: StateFlow<Decision?> = _prompt.asStateFlow()

    private val _linkedServer = MutableStateFlow<String?>(null)

    /** The server the driver agreed to, until Sign in has put it in its field ([linkUsed]). */
    val linkedServer: StateFlow<String?> = _linkedServer.asStateFlow()

    fun open(link: String) {
        _linkedServer.value = null
        scope.launch {
            val decision = ServerSetupLink.decide(link, signedInTo(), isManaged(), debuggable)
            when (decision) {
                is Decision.Confirm -> Unit
                is Decision.RefusedSignedIn -> Log.i(LOG_TAG, "Setup link not used: this phone belongs to a server.")
                Decision.RefusedManaged -> Log.i(LOG_TAG, "Setup link not used: the agency sets the server.")
                Decision.Invalid -> Log.i(LOG_TAG, "Setup link not used: no server address the app may use.")
            }
            _prompt.value = decision
        }
    }

    /**
     * Hands [server] to Sign in. Checked again here, because the driver may have signed in, or the agency set the
     * server, since the link arrived.
     */
    fun confirm(server: String) {
        _prompt.value = null
        scope.launch {
            if (signedInTo() != null || isManaged()) {
                Log.i(LOG_TAG, "Setup link not used: signed in or managed before it was confirmed.")
                return@launch
            }
            _linkedServer.value = server
        }
    }

    fun dismiss() {
        _prompt.value = null
    }

    /** Sign in has put [linkedServer] in its field. Cleared, so agreeing to the same link again fills it again. */
    fun linkUsed() {
        _linkedServer.value = null
    }

    /**
     * The server this phone belongs to, "" when it doesn't know which, or null when nothing on it belongs to one. A stored
     * trip belongs to the server it was started on, and can wait on Resume with no token at all, as after a restore from
     * backup, which keeps the trip but not the session. A token belongs to the server that issued it, signed in or not.
     */
    private suspend fun signedInTo(): String? {
        val session = sessionStore.session.first()
        val trip = tripStateStore.activeTrip.first()
        return when {
            trip != null -> trip.server(session.serverUrl).orEmpty()
            session.token != null -> session.serverUrl.orEmpty()
            else -> null
        }
    }

    private suspend fun isManaged(): Boolean = managedServerSource.server.first().url != null
}
