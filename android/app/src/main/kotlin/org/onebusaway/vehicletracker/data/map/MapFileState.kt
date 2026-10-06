package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.flow.StateFlow
import java.io.File

/** Where the agency's offline street map stands on this phone. */
sealed interface MapFileState {
    /** No street map: the agency has none, or the server has not been asked yet. */
    data object None : MapFileState

    /** The first copy is on its way. An update to a map already here downloads without showing this. */
    data class Downloading(val bytes: Long, val total: Long) : MapFileState

    data class Ready(val file: File) : MapFileState

    /** The first copy could not be fetched. The app tries again by itself. */
    data class Failed(val reason: MapFailure) : MapFileState
}

enum class MapFailure {
    /** The phone has too little free space for the file. */
    NO_SPACE,

    /** The server or the file's host could not be reached, or answered with an error. */
    NETWORK,

    /** What the host sent is not a complete vector map file. */
    INVALID_FILE,
}

/** The street map's state, as the tracking screen reads it. */
interface MapFileSource {
    val state: StateFlow<MapFileState>
}

/** How far a download has got, in whole percent. It shows 100 only once the file is done. */
fun downloadPercent(bytes: Long, total: Long): Int =
    if (total <= 0) 0 else (bytes * 100 / total).toInt().coerceIn(0, 99)
