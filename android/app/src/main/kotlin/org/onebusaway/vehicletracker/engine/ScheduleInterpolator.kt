package org.onebusaway.vehicletracker.engine

import java.time.Duration
import java.time.Instant

/**
 * Where the schedule says a vehicle should be: a port of the server's `rider.ScheduledOffsetAt`
 * (`rider/index.go`) on absolute times, by way of `ScheduleInterpolator.swift`. The scheduled time
 * at an along-shape distance is interpolated between the bracketing stops' departure and arrival,
 * and clamped to the first stop's arrival before it and the last stop's arrival after it.
 */
object ScheduleInterpolator {
    /** Null for a trip with no stops, where the Go function answers zero. */
    fun scheduledTime(along: Double, stops: List<TripStop>): Instant? {
        val first = stops.firstOrNull() ?: return null
        val last = stops.last()
        if (along <= first.alongShapeM) return first.arrivalAt
        if (along >= last.alongShapeM) return last.arrivalAt
        for (i in 1 until stops.size) {
            val a = stops[i - 1]
            val b = stops[i]
            if (along > b.alongShapeM) continue
            val span = b.alongShapeM - a.alongShapeM
            if (span <= 0) return a.departureAt
            val fraction = (along - a.alongShapeM) / span
            // Truncated to whole nanoseconds, as Go's float64-to-Duration conversion does.
            return a.departureAt.plusNanos((fraction * Duration.between(a.departureAt, b.arrivalAt).toNanos()).toLong())
        }
        return last.arrivalAt
    }
}
