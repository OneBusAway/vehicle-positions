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

    /** iOS's two T1 stops: ST3's 25:10 is written as 01:10 on the next calendar day. */
    const val T1_STOPS_JSON =
        """[{"id":"ST1","name":"Stop ST1","sequence":1,"lat":47.6,"lon":-122.33,"along_shape_m":0,""" +
            """"arrival_at":"2026-09-02T08:00:00-07:00","departure_at":"2026-09-02T08:00:00-07:00"},""" +
            """{"id":"ST3","name":"Stop ST3","sequence":3,"lat":47.609,"lon":-122.33,"along_shape_m":1001.2,""" +
            """"arrival_at":"2026-09-03T01:10:00-07:00","departure_at":"2026-09-03T01:10:00-07:00"}]"""

    /** The catalog's trip payload as the server renders it (spec §4.3), after iOS's `tripJSON`. */
    fun tripJson(
        id: String = "T1",
        serviceDate: String = "20260902",
        directionId: String = "0",
        points: String = "[[47.6,-122.33],[47.6045,-122.33],[47.609,-122.33]]",
        stops: String = T1_STOPS_JSON,
    ): String =
        """{"id":"$id","route_id":"R1","headsign":"North","direction_id":$directionId,""" +
            """"service_date":"$serviceDate","timezone":"America/Los_Angeles",""" +
            """"route":{"short_name":"1","long_name":"Straight","color":"0077C0","text_color":"FFFFFF"},""" +
            """"shape":{"length_m":1001.2,"points":$points},"stops":$stops,""" +
            """"thresholds":{"max_shape_distance_m":60,"schedule_early_s":900,"schedule_late_s":5400}}"""
}
