package org.onebusaway.vehicletracker.ui.map

import org.onebusaway.vehicletracker.engine.Geo
import kotlin.math.cos
import kotlin.math.log2
import kotlin.math.max

/**
 * How much road the map keeps between the vehicle and its top edge while following. iOS's phone
 * map centres 300 m ahead of the vehicle and spec §6.3 has CarPlay keeping about 800 m in view;
 * this sits between the two.
 */
const val ROAD_AHEAD_M = 500.0

const val MIN_FOLLOW_ZOOM = 10.0
const val MAX_FOLLOW_ZOOM = 18.0

/** Metres across one density-independent pixel at zoom 0 on the equator, with MapLibre's 512 px tiles. */
private const val METRES_PER_DP_AT_ZOOM_0 = 78_271.516964

/** Keeps the scale finite at the poles. */
private const val MIN_COS_LAT = 1e-6

/**
 * The zoom at which [ROAD_AHEAD_M] of road spans [aheadDp], the distance on screen from the
 * vehicle to the top of the map. A fixed zoom would not do: the same zoom shows half as much
 * ground again at the equator as it does at Seattle's latitude.
 */
fun followZoom(latitude: Double, aheadDp: Double): Double {
    val metresPerDp = ROAD_AHEAD_M / max(aheadDp, 1.0)
    val zoom = log2(METRES_PER_DP_AT_ZOOM_0 * max(cos(Geo.rad(latitude)), MIN_COS_LAT) / metresPerDp)
    return zoom.coerceIn(MIN_FOLLOW_ZOOM, MAX_FOLLOW_ZOOM)
}
