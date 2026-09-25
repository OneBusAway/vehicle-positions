package org.onebusaway.vehicletracker.engine

import org.onebusaway.vehicletracker.service.LocationFix
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime

/**
 * The server fixture's trip T1 — a straight 1 km run north with stops at 0, 500 and 1001 m
 * scheduled 08:00, 08:05, 08:10 Pacific on 2026-09-02 — and a loop whose start and end share a
 * point. A port of iOS's `TripFixtures`.
 */
object TripFixtures {
    val pacific: ZoneId = ZoneId.of("America/Los_Angeles")

    fun at(hour: Int, minute: Int, second: Int = 0, day: Int = 2): Instant =
        ZonedDateTime.of(2026, 9, day, hour, minute, second, 0, pacific).toInstant()

    private fun stop(id: String, lat: Double, lon: Double, alongShapeM: Double, time: Instant) =
        TripStop(id, "Stop $id", lat, lon, alongShapeM, arrivalAt = time, departureAt = time)

    val thresholds = AdherenceThresholds(maxShapeDistanceM = 60.0, scheduleEarlyS = 900, scheduleLateS = 5400)

    val t1 = TripGeometry(
        tripId = "T1",
        serviceDate = "20260902",
        timezone = pacific,
        shapePoints = listOf(GeoPoint(47.6000, -122.3300), GeoPoint(47.6045, -122.3300), GeoPoint(47.6090, -122.3300)),
        stops = listOf(
            stop("ST1", 47.6000, -122.3300, 0.0, at(8, 0)),
            stop("ST2", 47.6045, -122.3300, 500.0, at(8, 5)),
            stop("ST3", 47.6090, -122.3300, 1001.0, at(8, 10)),
        ),
        thresholds = thresholds,
    )

    /** A square loop, north then east then south then west, that ends where it starts. */
    val loop = TripGeometry(
        tripId = "L",
        serviceDate = "20260902",
        timezone = pacific,
        shapePoints = listOf(
            GeoPoint(47.6000, -122.3300),
            GeoPoint(47.6045, -122.3300),
            GeoPoint(47.6045, -122.3234),
            GeoPoint(47.6000, -122.3234),
            GeoPoint(47.6000, -122.3300),
        ),
        stops = listOf(
            stop("A", 47.6000, -122.3300, 0.0, at(9, 0)),
            stop("B", 47.6000, -122.3300, 1995.0, at(9, 20)),
        ),
        thresholds = thresholds,
    )

    fun fix(lat: Double, lon: Double, at: Instant, accuracy: Double? = 5.0, speed: Double? = 8.0) =
        LocationFix(latitude = lat, longitude = lon, bearing = 0.0, speed = speed, accuracy = accuracy, timeEpochSec = at.epochSecond)
}
