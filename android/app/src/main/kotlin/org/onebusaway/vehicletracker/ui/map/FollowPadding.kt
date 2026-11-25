package org.onebusaway.vehicletracker.ui.map

import org.onebusaway.vehicletracker.data.MapUp

/**
 * The camera padding, left, top, right and bottom as MapLibre takes it, that puts the vehicle a
 * third of the way up the part of a [mapHeight]-pixel map the screen leaves uncovered: the screen
 * draws over [coveredTop] pixels at the top and [coveredBottom] at the bottom. MapLibre centres the
 * vehicle in what the padding leaves, so the top padding takes a third of the uncovered map too.
 * With north up the road ahead can run any way, so the vehicle is in the middle of it.
 */
fun followPadding(mapHeight: Int, coveredTop: Int, coveredBottom: Int, up: MapUp): DoubleArray {
    val uncovered = (mapHeight - coveredTop - coveredBottom).coerceAtLeast(0)
    val behind = if (up == MapUp.NORTH) 0.0 else uncovered / 3.0
    return doubleArrayOf(0.0, coveredTop + behind, 0.0, coveredBottom.toDouble())
}

/** How much of the uncovered map is ahead of the vehicle, where [followPadding] puts it. */
fun shareAhead(up: MapUp): Float = if (up == MapUp.NORTH) 1f / 2 else 2f / 3
