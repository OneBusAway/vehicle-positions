package org.onebusaway.vehicletracker.engine

import org.onebusaway.vehicletracker.service.LocationFix
import java.time.Duration
import java.time.Instant
import kotlin.math.max

/** How the driver is keeping to the route and the schedule, judged from one position fix. */
enum class AdherenceStatus { ON_TIME, EARLY, LATE, OFF_SCHEDULE, OFF_ROUTE }

data class Adherence(
    val fix: LocationFix,
    val projection: Projection,
    val isOnRoute: Boolean,
    /** Where the schedule says the vehicle should be at this along-shape position. */
    val scheduledTime: Instant,
    /** Seconds behind schedule; negative is early. */
    val scheduleDeviationSec: Double,
    val nextStop: TripStop,
    /** Metres along the shape to the next stop. */
    val distanceToNextStopM: Double,
    /** Seconds to the next stop at the current speed. */
    val timeToNextStopSec: Double,
    val status: AdherenceStatus,
)

/**
 * Judges each fix against the trip's shape and schedule with the rules the server's rider engine
 * applies (spec §5.5): the on-route tolerance is the server's shape distance plus the fix's own
 * accuracy, and the schedule window is the server's. The display cut-offs for early and late are
 * this app's own. A port of `AdherenceEvaluator.swift`.
 */
class AdherenceEvaluator private constructor(
    val trip: TripGeometry,
    val shape: ShapeGeometry,
) {
    fun evaluate(fix: LocationFix, previous: Adherence?): Adherence {
        // The previous match keeps loops and out-and-backs from snapping to the wrong pass; an
        // off-route fix says nothing about where on the shape the vehicle is, so it does not
        // advance the hint.
        val hint = previous?.takeIf { it.isOnRoute }?.projection?.alongShape
        val projection = shape.project(GeoPoint(fix.latitude, fix.longitude), hint)

        val tolerance = trip.thresholds.maxShapeDistanceM + max(fix.accuracy ?: 0.0, 0.0)
        val isOnRoute = projection.distanceToShape <= tolerance

        val fixTime = Instant.ofEpochSecond(fix.timeEpochSec)
        val scheduledTime = ScheduleInterpolator.scheduledTime(projection.alongShape, trip.stops) ?: fixTime
        val deviation = Duration.between(scheduledTime, fixTime).toNanos() / NANOS_PER_SECOND

        val nextStop = trip.stops.firstOrNull { it.alongShapeM > projection.alongShape } ?: trip.stops.last()
        val distance = max(0.0, nextStop.alongShapeM - projection.alongShape)
        val time = distance / max(fix.speed ?: 0.0, MINIMUM_SPEED_MPS)

        val status = when {
            !isOnRoute -> AdherenceStatus.OFF_ROUTE
            deviation < -trip.thresholds.scheduleEarlyS.toDouble() ||
                deviation > trip.thresholds.scheduleLateS.toDouble() -> AdherenceStatus.OFF_SCHEDULE
            deviation < EARLY_THRESHOLD_SEC -> AdherenceStatus.EARLY
            deviation > LATE_THRESHOLD_SEC -> AdherenceStatus.LATE
            else -> AdherenceStatus.ON_TIME
        }

        return Adherence(
            fix = fix,
            projection = projection,
            isOnRoute = isOnRoute,
            scheduledTime = scheduledTime,
            scheduleDeviationSec = deviation,
            nextStop = nextStop,
            distanceToNextStopM = distance,
            timeToNextStopSec = time,
            status = status,
        )
    }

    companion object {
        /** More than a minute ahead of schedule shows as early. */
        const val EARLY_THRESHOLD_SEC = -60.0

        /** More than five minutes behind shows as late. */
        const val LATE_THRESHOLD_SEC = 300.0

        /**
         * Speed floor for the time-to-next-stop estimate, so a stopped or speed-less fix still
         * yields a finite time.
         */
        const val MINIMUM_SPEED_MPS = 3.0

        private const val NANOS_PER_SECOND = 1e9

        /** Null for a trip with no usable shape or no stops: nothing to judge against. */
        fun of(trip: TripGeometry): AdherenceEvaluator? {
            val shape = ShapeGeometry.of(trip.shapePoints) ?: return null
            if (trip.stops.isEmpty()) return null
            return AdherenceEvaluator(trip, shape)
        }
    }
}
