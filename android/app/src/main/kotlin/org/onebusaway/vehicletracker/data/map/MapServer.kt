package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CancellationException
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import retrofit2.HttpException
import javax.inject.Inject

/** What the server says about the agency's map file. */
sealed interface MapServerAnswer {
    data class File(val url: String) : MapServerAnswer

    /** The agency has no map file, or the server is older than `GET /api/v1/map` and answers 404. */
    data object NoMap : MapServerAnswer

    /** The server turned the driver's token down; asking again before they sign in is pointless. */
    data object SignedOut : MapServerAnswer

    /** No answer: no network, a server error, or a reply that is not one. */
    data object Unreachable : MapServerAnswer
}

/** Asks the server where the agency's map file is. An interface so [MapRepository] can be tested without one. */
fun interface MapServer {
    suspend fun ask(): MapServerAnswer
}

class TrackerMapServer @Inject constructor(private val apiProvider: TrackerApiProvider) : MapServer {
    override suspend fun ask(): MapServerAnswer = try {
        apiProvider.get().mapConfig().pmtilesUrl?.takeIf { it.isNotBlank() }
            ?.let { MapServerAnswer.File(it) }
            ?: MapServerAnswer.NoMap
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
