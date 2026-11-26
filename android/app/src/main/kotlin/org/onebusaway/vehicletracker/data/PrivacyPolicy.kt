package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.BoundApi
import retrofit2.HttpException
import java.io.IOException
import java.net.URI
import java.net.URISyntaxException

private const val TAG = "PrivacyPolicy"

/** The agency's privacy policy link. An interface so the Privacy page can be tested without a server. */
fun interface PrivacyPolicySource {
    /** Null when there is none to open. */
    suspend fun link(): String?
}

/**
 * Asks the server signed in, at `GET /api/v1/agency`. There is no link to open when the agency gives none, when the
 * server is older than the link and answers 404, when it can't be reached, or when the link is not https: it opens in
 * the browser as it is, but for its scheme.
 */
class TrackerPrivacyPolicy(private val boundApi: () -> BoundApi?) : PrivacyPolicySource {
    override suspend fun link(): String? = try {
        boundApi()?.api?.agency()?.privacyPolicyUrl?.let(::httpsLink)
    } catch (e: CancellationException) {
        throw e
    } catch (e: HttpException) {
        if (e.code() != HTTP_NOT_FOUND) AppLog.w(TAG, "The server did not give the privacy policy link: ${e.code()}")
        null
    } catch (e: IOException) {
        // No connection: the page goes without the link this time.
        null
    } catch (e: Exception) {
        AppLog.w(TAG, "Could not read the privacy policy link", e)
        null
    }
}

private const val HTTP_NOT_FOUND = 404

/**
 * [url] if it is an https address with a host, with its scheme in lower case: Android matches an intent's scheme as it
 * is written, so `HTTPS://` would find no browser. Null for anything else.
 */
internal fun httpsLink(url: String): String? = try {
    val uri = URI(url)
    if (uri.scheme.equals("https", ignoreCase = true) && !uri.host.isNullOrEmpty()) "https" + url.substring(uri.scheme.length) else null
} catch (e: URISyntaxException) {
    null
}
