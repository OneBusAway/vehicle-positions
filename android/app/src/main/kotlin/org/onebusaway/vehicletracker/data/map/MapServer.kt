package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.BoundApi
import org.onebusaway.vehicletracker.data.normalizedServerUrl
import retrofit2.HttpException

/** What the server says about the agency's map file. */
sealed interface MapServerAnswer {
    data class File(val url: String) : MapServerAnswer

    /** The agency has no map file, or the server is older than `GET /api/v1/map` and answers 404. */
    data object NoMap : MapServerAnswer

    /** The server turned the driver's token down; asking again before they sign in is pointless. */
    data object SignedOut : MapServerAnswer

    /** No answer: no network, a server error, or a reply that is not one. */
    data object Unreachable : MapServerAnswer

    /** Not asked: the app's API client is still set up for another server, as for a moment after a sign-in. */
    data object OtherServer : MapServerAnswer
}

/** Asks the server where the agency's map file is. An interface so [MapRepository] can be tested without one. */
fun interface MapServer {
    /** Asks [server], as [normalizedServerUrl] gives it, and no other. */
    suspend fun ask(server: String): MapServerAnswer
}

/**
 * Asks through the app's API client, and only while it is set up for the server asked about. The client follows the
 * session a moment behind, so just after a sign-in it can still send to the last server: that server's answer would be
 * saved as the new one's map, and if it refused the old token, the request would go to it again with the new one.
 */
class TrackerMapServer(private val boundApi: () -> BoundApi?) : MapServer {
    override suspend fun ask(server: String): MapServerAnswer = try {
        val bound = boundApi()
        if (bound == null || normalizedServerUrl(bound.serverUrl) != server) {
            MapServerAnswer.OtherServer
        } else {
            bound.api.mapConfig().pmtilesUrl?.takeIf { it.isNotBlank() }
                ?.let { MapServerAnswer.File(it) }
                ?: MapServerAnswer.NoMap
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: HttpException) {
        when (e.code()) {
            404 -> MapServerAnswer.NoMap
            401 -> MapServerAnswer.SignedOut
            else -> MapServerAnswer.Unreachable
        }
    } catch (e: Exception) {
        MapServerAnswer.Unreachable
    }
}
