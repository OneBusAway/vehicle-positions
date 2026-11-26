package org.onebusaway.vehicletracker.data.map

import java.util.concurrent.atomic.AtomicInteger

/**
 * How many maps are up in this process, from when one is made until it is destroyed. A map file may be deleted while
 * none is: MapLibre crashes if a file it has open goes (maplibre-native #3658). Otherwise the file waits for the next
 * start, as a replaced one does.
 */
object OpenMaps {
    private val count = AtomicInteger()

    fun opened() {
        count.incrementAndGet()
    }

    fun closed() {
        count.decrementAndGet()
    }

    val none: Boolean get() = count.get() == 0
}
