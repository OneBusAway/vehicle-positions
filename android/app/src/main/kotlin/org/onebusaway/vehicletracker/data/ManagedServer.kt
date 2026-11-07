package org.onebusaway.vehicletracker.data

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.RestrictionsManager
import android.util.Log
import androidx.core.content.ContextCompat
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.onEach

/** The key in the app's managed configuration that names the agency's server (`res/xml/app_restrictions.xml`). */
internal const val MANAGED_SERVER_URL_KEY = "server_url"

/**
 * The server the agency's device management sets: [url] when it is one the app may use. [invalid] when the agency set
 * one it may not, which is then ignored, and the driver types one instead.
 */
data class ManagedServer(val url: String?, val invalid: Boolean)

internal fun managedServerOf(raw: String?, debuggable: Boolean): ManagedServer {
    if (raw.isNullOrBlank()) return ManagedServer(url = null, invalid = false)
    val url = presetServerOf(raw, debuggable) ?: return ManagedServer(url = null, invalid = true)
    return ManagedServer(url = url.toString(), invalid = false)
}

interface ManagedServerSource {
    /** The current value, then each change, for as long as it is collected. */
    val server: Flow<ManagedServer>
}

private const val LOG_TAG = "ManagedServer"

/**
 * Reads `server_url` from the managed configuration a device owner or profile owner sets. The platform announces a
 * change only to receivers registered at runtime, so one is registered for as long as [server] is collected.
 */
class RestrictionsManagedServerSource(private val context: Context) : ManagedServerSource {
    override val server: Flow<ManagedServer> = callbackFlow {
        val restrictions = context.getSystemService(RestrictionsManager::class.java)
        val debuggable = context.isDebuggable
        fun current() = managedServerOf(restrictions?.applicationRestrictions?.getString(MANAGED_SERVER_URL_KEY), debuggable)

        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                trySend(current())
            }
        }
        // Registered before the first read, so a change in between is not missed.
        ContextCompat.registerReceiver(
            context,
            receiver,
            IntentFilter(Intent.ACTION_APPLICATION_RESTRICTIONS_CHANGED),
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )
        trySend(current())
        awaitClose { context.unregisterReceiver(receiver) }
    }
        .distinctUntilChanged()
        .onEach { if (it.invalid) Log.w(LOG_TAG, "Ignoring the managed server_url: not an address the app may use.") }
}
