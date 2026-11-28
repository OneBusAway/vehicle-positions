package org.onebusaway.vehicletracker.data.map

/**
 * How many maps are up in this process, from when one is made until it is destroyed. A map file may be deleted while
 * none is: MapLibre crashes if a file it has open goes (maplibre-native #3658). Otherwise the file waits for the next
 * start, as a replaced one does.
 */
object OpenMaps {
    private val lock = Any()
    private var count = 0

    /** Waits for a delete under way, so a map never comes up halfway through one. */
    fun opened() {
        synchronized(lock) { count++ }
    }

    fun closed() {
        synchronized(lock) { count-- }
    }

    /**
     * Runs [delete] if no map is up, and holds off any map from coming up until it is done. Returns false, having run
     * nothing, while one is up.
     */
    fun whileNoneOpen(delete: () -> Unit): Boolean = synchronized(lock) {
        if (count != 0) return false
        delete()
        true
    }
}
