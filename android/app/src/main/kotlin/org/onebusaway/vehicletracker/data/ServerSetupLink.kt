package org.onebusaway.vehicletracker.data

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.net.URI
import java.net.URISyntaxException
import java.net.URLDecoder

/**
 * `obavehicletracker://setup?server=<percent-encoded address>`, sent by the agency as a link or a QR code so drivers
 * don't type the server address. Anyone can send a driver a link, so one only ever fills in Sign in's server field,
 * after the driver agrees; it never signs in.
 *
 * Read with [URI] rather than `android.net.Uri`, which is a stub on the JVM, so the tests run this code.
 */
object ServerSetupLink {
    private const val SCHEME = "obavehicletracker"
    private const val HOST = "setup"
    private const val SERVER = "server"

    sealed interface Decision {
        /**
         * Ask "Use [host]?" and fill in [server] only if the driver agrees. The host, not the whole address, so the
         * driver reads where the app would go; it is [HttpUrl.host], which is punycode for a look-alike name rather
         * than the name it imitates.
         */
        data class Confirm(val server: String, val host: String) : Decision

        /**
         * A phone that is signed in, or has a trip stored, keeps its server: its token and its trip belong there. [host]
         * is null for a trip on a phone that doesn't know its server, as after a restore from backup, which keeps the
         * trip but not the session.
         */
        data class RefusedSignedIn(val host: String?) : Decision
        data object RefusedManaged : Decision
        data object Invalid : Decision
    }

    /** The server [link] names, if it is one the app may use. Only the host `setup` with the one query item `server`. */
    fun parse(link: String, debuggable: Boolean): HttpUrl? {
        val uri = try {
            URI(link)
        } catch (e: URISyntaxException) {
            return null
        }
        if (!SCHEME.equals(uri.scheme, ignoreCase = true) || !HOST.equals(uri.host, ignoreCase = true)) return null
        val item = uri.rawQuery?.split('&')?.singleOrNull()?.split('=', limit = 2) ?: return null
        if (item.size != 2 || decoded(item[0]) != SERVER) return null
        return decoded(item[1])?.let { presetServerOf(it, debuggable) }
    }

    /** [signedInTo] is the server the phone belongs to, "" when it doesn't know which, or null when it belongs to none. */
    fun decide(link: String, signedInTo: String?, managed: Boolean, debuggable: Boolean): Decision {
        if (signedInTo != null) return Decision.RefusedSignedIn(signedInTo.toHttpUrlOrNull()?.host)
        if (managed) return Decision.RefusedManaged
        val server = parse(link, debuggable) ?: return Decision.Invalid
        return Decision.Confirm(server.toString(), server.host)
    }

    /** Percent-decoded, as a query item is. A "+" stays a plus, as it does on iOS. Null when the encoding is broken. */
    private fun decoded(part: String): String? = try {
        URLDecoder.decode(part.replace("+", "%2B"), "UTF-8")
    } catch (e: IllegalArgumentException) {
        null
    }
}
