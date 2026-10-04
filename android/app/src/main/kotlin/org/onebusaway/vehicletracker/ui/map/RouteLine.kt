package org.onebusaway.vehicletracker.ui.map

import org.maplibre.geojson.LineString
import org.maplibre.geojson.Point
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.ui.routes.routeColorArgb
import kotlin.math.roundToInt

/** iOS's `UIColor.systemBlue`, the line colour spec §6.3 falls back to when the feed gives none. */
const val FALLBACK_ROUTE_COLOR = 0xFF007AFF.toInt()

/** How much darker than the route colour the casing under the line is drawn (spec §6.3). */
const val CASING_DARKENING = 0.35

private const val ALPHA_MASK = 0xFF000000.toInt()

/**
 * The shape as a GeoJSON line, or null for fewer than two points, which is no line at all. A
 * GeoJSON position is longitude first, the reverse of [GeoPoint] and of the server's `[lat, lon]`.
 */
fun routeLineString(points: List<GeoPoint>): LineString? =
    if (points.size < 2) null else LineString.fromLngLats(points.map { Point.fromLngLat(it.lon, it.lat) })

/** The route line's ARGB colour: the feed's `route_color`, or [FALLBACK_ROUTE_COLOR] when it has none. */
fun routeLineColor(hex: String): Int = routeColorArgb(hex)?.toInt() ?: FALLBACK_ROUTE_COLOR

/**
 * The casing drawn wider under the route line, so a bright route colour still reads over pale
 * streets: iOS's `darkened(by:)`, which cuts HSB brightness and keeps hue and saturation. Holding
 * those two, brightness scales every channel by the same factor, so scaling the channels directly
 * gives the same colour without the round trip through HSB.
 */
fun casingColor(routeArgb: Int): Int {
    val keep = 1 - CASING_DARKENING
    fun channel(shift: Int) = (((routeArgb ushr shift) and 0xFF) * keep).roundToInt() shl shift
    return (routeArgb and ALPHA_MASK) or channel(16) or channel(8) or channel(0)
}
