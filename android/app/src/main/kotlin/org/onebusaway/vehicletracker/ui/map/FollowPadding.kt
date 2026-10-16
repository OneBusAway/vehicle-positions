package org.onebusaway.vehicletracker.ui.map

/**
 * The camera padding, left, top, right and bottom as MapLibre takes it, that puts the vehicle a
 * third of the way up the part of a [mapHeight]-pixel map the screen leaves uncovered: the screen
 * draws over [coveredTop] pixels at the top and [coveredBottom] at the bottom. MapLibre centres the
 * vehicle in what the padding leaves, so the top padding takes a third of the uncovered map too.
 */
fun followPadding(mapHeight: Int, coveredTop: Int, coveredBottom: Int): DoubleArray {
    val uncovered = (mapHeight - coveredTop - coveredBottom).coerceAtLeast(0)
    return doubleArrayOf(0.0, coveredTop + uncovered / 3.0, 0.0, coveredBottom.toDouble())
}
