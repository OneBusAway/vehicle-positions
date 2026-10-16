package org.onebusaway.vehicletracker.ui.map

import org.maplibre.geojson.LineString
import org.maplibre.geojson.Point
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.ui.routes.routeColorArgb
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow
import kotlin.math.roundToInt

/** iOS's `UIColor.systemBlue`, the line colour spec §6.3 falls back to when the feed gives none. */
const val FALLBACK_ROUTE_COLOR = 0xFF007AFF.toInt()

private const val ALPHA_MASK = 0xFF000000.toInt()
private const val WHITE = 0xFFFFFFFF.toInt()
private const val NIGHT_INK = 0xFF0A0A0A.toInt()

/**
 * The design's cut-offs: a route brighter than these, by WCAG luminance, gets a dark casing on the
 * light and the dark map, where a white one would melt into the line. Darker routes get white.
 */
private const val LIGHT_MAP_DARK_CASING_ABOVE = 0.35
private const val DARK_MAP_DARK_CASING_ABOVE = 0.18

/** How far a route line stands from the dark map: a line, like any graphic, needs 3:1. */
private const val NIGHT_ROUTE_CONTRAST = 3.0
private const val LIGHTNESS_STEP = 0.01

/**
 * The shape as a GeoJSON line, or null for fewer than two points, which is no line at all. A
 * GeoJSON position is longitude first, the reverse of [GeoPoint] and of the server's `[lat, lon]`.
 */
fun routeLineString(points: List<GeoPoint>): LineString? =
    if (points.size < 2) null else LineString.fromLngLats(points.map { Point.fromLngLat(it.lon, it.lat) })

/**
 * The route line's ARGB colour: the feed's `route_color`, or [FALLBACK_ROUTE_COLOR] when it has none.
 * On the [dark] map a route too dark to see is lifted until it reads.
 */
fun routeLineColor(hex: String, dark: Boolean): Int {
    val color = routeColorArgb(hex)?.toInt() ?: FALLBACK_ROUTE_COLOR
    return if (dark) liftedForNight(color) else color
}

/**
 * The casing drawn wider under the route line, so the line reads over the streets whatever its
 * colour: ink under a bright route, white under a dark one, with the design's cut-off for each map.
 */
fun casingColor(routeArgb: Int, dark: Boolean): Int {
    val luminance = luminance(routeArgb)
    return when {
        dark -> if (luminance > DARK_MAP_DARK_CASING_ABOVE) NIGHT_INK else WHITE
        else -> if (luminance > LIGHT_MAP_DARK_CASING_ABOVE) MARKER_INK else WHITE
    }
}

/**
 * [argb] lighter, with its hue and saturation kept, until it stands 3:1 from the dark map: a navy,
 * maroon or black route would be lost on it. A route that already reads comes back as it was.
 */
fun liftedForNight(argb: Int): Int {
    val (hue, saturation, start) = hsl(argb)
    var lightness = start
    var color = argb
    while (contrast(color, PLAIN_BACKGROUND_DARK) < NIGHT_ROUTE_CONTRAST && lightness < 1.0) {
        lightness = (lightness + LIGHTNESS_STEP).coerceAtMost(1.0)
        color = fromHsl(hue, saturation, lightness)
    }
    return color
}

/** WCAG relative luminance. */
internal fun luminance(argb: Int): Double {
    fun linear(shift: Int): Double {
        val c = ((argb ushr shift) and 0xFF) / 255.0
        return if (c <= 0.03928) c / 12.92 else ((c + 0.055) / 1.055).pow(2.4)
    }
    return 0.2126 * linear(16) + 0.7152 * linear(8) + 0.0722 * linear(0)
}

/** WCAG contrast ratio, 1 to 21. */
internal fun contrast(a: Int, b: Int): Double {
    val la = luminance(a)
    val lb = luminance(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

private fun hsl(argb: Int): Triple<Double, Double, Double> {
    val r = ((argb ushr 16) and 0xFF) / 255.0
    val g = ((argb ushr 8) and 0xFF) / 255.0
    val b = (argb and 0xFF) / 255.0
    val high = maxOf(r, g, b)
    val low = minOf(r, g, b)
    val lightness = (high + low) / 2
    val delta = high - low
    if (delta == 0.0) return Triple(0.0, 0.0, lightness)
    val saturation = delta / (1 - abs(2 * lightness - 1))
    val hue = when (high) {
        r -> 60 * (((g - b) / delta).mod(6.0))
        g -> 60 * ((b - r) / delta + 2)
        else -> 60 * ((r - g) / delta + 4)
    }
    return Triple(hue, saturation, lightness)
}

private fun fromHsl(hue: Double, saturation: Double, lightness: Double): Int {
    val chroma = (1 - abs(2 * lightness - 1)) * saturation
    val x = chroma * (1 - abs((hue / 60).mod(2.0) - 1))
    val m = lightness - chroma / 2
    val (r, g, b) = when ((hue / 60).toInt()) {
        0 -> Triple(chroma, x, 0.0)
        1 -> Triple(x, chroma, 0.0)
        2 -> Triple(0.0, chroma, x)
        3 -> Triple(0.0, x, chroma)
        4 -> Triple(x, 0.0, chroma)
        else -> Triple(chroma, 0.0, x)
    }
    fun channel(value: Double) = ((value + m) * 255).roundToInt().coerceIn(0, 255)
    return ALPHA_MASK or (channel(r) shl 16) or (channel(g) shl 8) or channel(b)
}
