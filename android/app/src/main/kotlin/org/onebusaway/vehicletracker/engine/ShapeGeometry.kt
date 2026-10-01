package org.onebusaway.vehicletracker.engine

import kotlin.math.cos
import kotlin.math.hypot
import kotlin.math.max
import kotlin.math.min

/** The result of projecting a point onto a shape. */
data class Projection(
    /** Metres from the shape start. */
    val alongShape: Double,
    /** Metres from the point to the closest point on the shape. */
    val distanceToShape: Double,
)

/**
 * A polyline with precomputed cumulative distances: a port of the server's `rider.ShapeGeom`
 * (`rider/shape.go`), by way of `ShapeGeometry.swift`, including its hinted projection that keeps
 * loops and out-and-backs from snapping to the wrong pass.
 */
class ShapeGeometry private constructor(val points: List<GeoPoint>) {
    /** cumulative[i] = metres from points[0] to points[i]. */
    val cumulative: List<Double>
    val length: Double

    /** The local-projection origin, points[0]. */
    private val origin = points[0]

    /** The cosine of the mean latitude. */
    private val cosLat = cos(Geo.rad(points.sumOf { it.lat } / points.size))

    // localX and localY are the points projected onto the local plane once, at construction:
    // project runs on every fix, and re-deriving the vertices from degrees on each call was the
    // bulk of its work.
    private val localX = DoubleArray(points.size)
    private val localY = DoubleArray(points.size)

    init {
        val cumulative = DoubleArray(points.size)
        for (i in 1 until points.size) {
            cumulative[i] = cumulative[i - 1] + Geo.distance(points[i - 1], points[i])
        }
        this.cumulative = cumulative.asList()
        length = cumulative[points.size - 1]

        for ((i, p) in points.withIndex()) {
            val (x, y) = local(p)
            localX[i] = x
            localY[i] = y
        }
    }

    /**
     * The position of [p] along the shape. Distances are measured in a local equirectangular
     * projection. When [hint] is null the globally closest segment wins, found in one pass that
     * keeps nothing. With a hint — the path every fix after a trip's first match takes — the local
     * minimum nearest the hinted distance along the shape wins instead, backwards distance counting
     * for more than forwards, which keeps loops and out-and-backs from snapping to the wrong pass;
     * that needs every segment's distance at once, so it keeps the two arrays.
     */
    fun project(p: GeoPoint, hint: Double?): Projection {
        val segments = points.size - 1
        val (px, py) = local(p)

        if (hint == null) {
            var best = Projection(alongShape = 0.0, distanceToShape = Double.POSITIVE_INFINITY)
            for (i in 0 until segments) {
                val onto = projectOnto(i, px, py)
                if (onto.distanceToShape < best.distanceToShape) best = onto
            }
            return best
        }

        val dists = DoubleArray(segments)
        val alongs = DoubleArray(segments)
        var best = 0
        for (i in 0 until segments) {
            val onto = projectOnto(i, px, py)
            dists[i] = onto.distanceToShape
            alongs[i] = onto.alongShape
            if (dists[i] < dists[best]) best = i
        }

        var chosen = best
        val threshold = max(2 * dists[best] + 1, HINT_CANDIDATE_BAND)
        var closest = Double.POSITIVE_INFINITY
        for (i in 0 until segments) {
            if (dists[i] > threshold || !isLocalMin(dists, i)) continue
            var delta = alongs[i] - hint
            if (delta < 0) delta = -delta * BACKWARD_HINT_WEIGHT
            if (delta < closest) {
                closest = delta
                chosen = i
            }
        }
        return Projection(alongShape = alongs[chosen], distanceToShape = dists[chosen])
    }

    /** The coordinate [along] metres into the shape, clamped to [0, length]. */
    fun pointAt(along: Double): GeoPoint {
        val (i, t) = segmentAt(along)
        val a = points[i]
        val b = points[i + 1]
        return GeoPoint(a.lat + t * (b.lat - a.lat), a.lon + t * (b.lon - a.lon))
    }

    /** The bearing of the segment containing [along]. */
    fun bearingAt(along: Double): Double {
        val (i, _) = segmentAt(along)
        return Geo.initialBearing(points[i], points[i + 1])
    }

    /**
     * The projection of the local-plane point ([px], [py]) onto segment [i]: its distance from the
     * segment, and the along-shape distance of the closest point on it.
     */
    private fun projectOnto(i: Int, px: Double, py: Double): Projection {
        val ax = localX[i]
        val ay = localY[i]
        val dx = localX[i + 1] - ax
        val dy = localY[i + 1] - ay

        var t = 0.0
        val lenSq = dx * dx + dy * dy
        if (lenSq > 0) t = min(1.0, max(0.0, ((px - ax) * dx + (py - ay) * dy) / lenSq))
        return Projection(
            alongShape = cumulative[i] + t * (cumulative[i + 1] - cumulative[i]),
            distanceToShape = hypot(px - (ax + t * dx), py - (ay + t * dy)),
        )
    }

    /**
     * The index of the segment containing [along] and the fraction into that segment, clamping
     * [along] to [0, length].
     */
    private fun segmentAt(along: Double): Pair<Int, Double> {
        val last = points.size - 2
        if (along >= length) return last to 1.0
        if (along <= 0) return 0 to 0.0
        for (i in 0..last) {
            if (along < cumulative[i + 1]) {
                val segLen = cumulative[i + 1] - cumulative[i]
                if (segLen <= 0) return i to 0.0
                return i to (along - cumulative[i]) / segLen
            }
        }
        return last to 1.0
    }

    /** [p] in metres in the shape's equirectangular projection. */
    private fun local(p: GeoPoint): Pair<Double, Double> = Pair(
        (p.lon - origin.lon) * cosLat * Geo.METRES_PER_DEGREE,
        (p.lat - origin.lat) * Geo.METRES_PER_DEGREE,
    )

    companion object {
        /**
         * The minimum width, in metres, of the band of local minima a hint may choose between. It
         * lets a hint choose between passes of a loop that share a point, where the closest
         * distance is near zero and a purely proportional band would admit only the one pass.
         */
        private const val HINT_CANDIDATE_BAND = 30.0

        /**
         * How much further behind the hint a candidate pass must be, relative to one ahead of it,
         * before it wins. A vehicle on its trip moves forward along the shape: just past the
         * turnaround of an out-and-back the two legs are equally near the last match, and the one
         * ahead is the right one. A short step backwards on the same pass — GPS jitter, a bus
         * reversing into a bay — still beats a pass far ahead.
         */
        private const val BACKWARD_HINT_WEIGHT = 2.0

        /** Null for fewer than two points, which is no shape at all; the Go constructor panics. */
        fun of(points: List<GeoPoint>): ShapeGeometry? =
            if (points.size < 2) null else ShapeGeometry(points.toList())
    }
}

/** Whether dists[i] is no greater than its neighbours. */
private fun isLocalMin(dists: DoubleArray, i: Int): Boolean {
    if (i > 0 && dists[i] > dists[i - 1]) return false
    if (i < dists.size - 1 && dists[i] > dists[i + 1]) return false
    return true
}
