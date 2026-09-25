package org.onebusaway.vehicletracker.engine

import java.time.Instant
import java.time.ZoneId

/**
 * One trip as the phone judges adherence against it (spec §5.5): the shape, the stops with
 * absolute scheduled times, and the thresholds the server applies, so the phone judges by the
 * same numbers. Read from `GET /api/v1/gtfs/trips/{trip_id}`.
 */
data class TripGeometry(
    val tripId: String,
    /** The service date the stop times fall on, YYYYMMDD. */
    val serviceDate: String,
    /** The agency's zone: every clock time the driver is shown is read in it. */
    val timezone: ZoneId,
    val shapePoints: List<GeoPoint>,
    val stops: List<TripStop>,
    val thresholds: AdherenceThresholds,
)

data class TripStop(
    val id: String,
    val name: String,
    val lat: Double,
    val lon: Double,
    val alongShapeM: Double,
    val arrivalAt: Instant,
    val departureAt: Instant,
)

/** The server's own adherence rules; the phone's display cut-offs live in [AdherenceEvaluator]. */
data class AdherenceThresholds(
    val maxShapeDistanceM: Double,
    val scheduleEarlyS: Int,
    val scheduleLateS: Int,
)
