package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.flow.StateFlow
import java.io.File

/** Where the agency's offline street map stands on this phone. */
sealed interface MapFileState {
    /** No street map: the agency has none, or the server has not been asked yet. */
    data object None : MapFileState

    /** The first copy is on its way. An update to a map already here downloads without showing this. */
    data class Downloading(val bytes: Long, val total: Long) : MapFileState

    /**
     * [update] is a newer copy on its way, while maps keep [file]. [againFailed] is why the new copy the driver asked for
     * did not come, while maps keep this one; a check the app makes by itself never sets it. [deleteFailed] is a delete
     * the driver asked for that could not be saved, so the map is still here.
     */
    data class Ready(
        val file: File,
        val update: Downloading? = null,
        val againFailed: Failed? = null,
        val deleteFailed: Boolean = false,
    ) : MapFileState

    /**
     * The first copy could not be fetched. When the app tries again depends on [reason]. [size] is the file's, in bytes,
     * when its host said: with too little space, what the map needs.
     */
    data class Failed(val reason: MapFailure, val size: Long? = null) : MapFileState

    /** The driver deleted it from this phone, and no refresh gets it again until they ask for it. */
    data object Deleted : MapFileState
}

enum class MapFailure {
    /** The phone has too little free space for the file. */
    NO_SPACE,

    /** The server or the file's host could not be reached, or answered with an error. */
    NETWORK,

    /** What the host sent is not a complete vector map file. */
    INVALID_FILE,

    /**
     * The URL, or one it redirects to, is plain http, and the phone will not send cleartext to its
     * host: a release build refuses it for every host. Trying again cannot help.
     */
    CLEARTEXT_BLOCKED,

    /** Anything else, such as `map.json` failing to be read or saved. It is logged, and the next refresh tries again. */
    UNEXPECTED,
}

/** The street map's state, as the tracking screen reads it. */
interface MapFileSource {
    val state: StateFlow<MapFileState>
}

/** The map on the phone: how big its file is, when it was fetched, and when its host was last asked about it. */
data class MapDetails(val bytes: Long, val fetchedAtMs: Long, val checkedAtEpochSec: Long)

/** The street map as Settings shows it, and what the driver can do with it there. */
interface OfflineMapSource : MapFileSource {
    /** The map on the phone for the server signed in; null when there is none, as one from another server is not its. */
    suspend fun details(): MapDetails?

    fun refresh()

    fun delete()

    fun getAgain()
}

/** How far a download has got, in whole percent. It shows 100 only once the file is done. */
fun downloadPercent(bytes: Long, total: Long): Int =
    if (total <= 0) 0 else (bytes * 100 / total).toInt().coerceIn(0, 99)
