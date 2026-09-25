package org.onebusaway.vehicletracker.engine

import kotlin.math.PI
import kotlin.math.asin
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.min
import kotlin.math.sin
import kotlin.math.sqrt

/**
 * Great-circle helpers: the same formulas and constants as the server's `rider/shape.go`, so the
 * phone's distances agree with the server's.
 */
object Geo {
    /** The mean Earth radius used by the haversine formula. */
    const val EARTH_RADIUS_M = 6_371_000.0

    /** The local equirectangular scale factor. */
    const val METRES_PER_DEGREE = 111_320.0

    /** The great-circle distance between [a] and [b] in metres. */
    fun distance(a: GeoPoint, b: GeoPoint): Double {
        val lat1 = rad(a.lat)
        val lat2 = rad(b.lat)
        val dLat = lat2 - lat1
        val dLon = rad(b.lon - a.lon)
        val h = sin(dLat / 2) * sin(dLat / 2) + cos(lat1) * cos(lat2) * sin(dLon / 2) * sin(dLon / 2)
        return 2 * EARTH_RADIUS_M * asin(min(1.0, sqrt(h)))
    }

    /** The initial great-circle bearing from [a] to [b], degrees clockwise from north in [0, 360). */
    fun initialBearing(a: GeoPoint, b: GeoPoint): Double {
        val lat1 = rad(a.lat)
        val lat2 = rad(b.lat)
        val dLon = rad(b.lon - a.lon)
        val y = sin(dLon) * cos(lat2)
        val x = cos(lat1) * sin(lat2) - sin(lat1) * cos(lat2) * cos(dLon)
        val deg = atan2(y, x) * 180 / PI
        return (deg + 360) % 360
    }

    internal fun rad(deg: Double): Double = deg * PI / 180
}
