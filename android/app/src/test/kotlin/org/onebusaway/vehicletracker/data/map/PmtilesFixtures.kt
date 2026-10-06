package org.onebusaway.vehicletracker.data.map

import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * A PMTiles version 3 file [size] bytes long whose header accounts for every byte: a 10-byte root
 * directory, 10 bytes of metadata, no leaf directories and tile data to the end. The tile bytes
 * are a counting pattern, so a test can tell a spliced file from a whole one. Not a map MapLibre
 * could draw, but everything [headerMatchesLength] checks.
 */
fun pmtiles(size: Int = 4096, tileType: Int = 1): ByteArray {
    require(size > HEADER_BYTES + 20)
    val bytes = ByteArray(size) { (it % 251).toByte() }
    val header = ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN)
    "PMTiles".toByteArray(Charsets.US_ASCII).forEachIndexed { i, b -> header.put(i, b) }
    header.put(7, 3)
    val root = HEADER_BYTES.toLong()
    header.putLong(8, root).putLong(16, 10)
    header.putLong(24, root + 10).putLong(32, 10)
    header.putLong(40, root + 20).putLong(48, 0)
    header.putLong(56, root + 20).putLong(64, size - root - 20)
    header.put(99, tileType.toByte())
    return bytes
}
