import Foundation
import VehiclePositionsKit
@testable import VehicleTracker

/// The server fixture's trip T1: a straight 1 km run north with stops at 0,
/// 500 and 1001 m scheduled 08:00, 08:05, 08:10 Pacific on 2026-09-02.
nonisolated enum TripFixtures {
    static let pacific = TimeZone(identifier: "America/Los_Angeles")!

    static func at(_ hour: Int, _ minute: Int, _ second: Int = 0, day: Int = 2) -> Date {
        var c = DateComponents()
        c.year = 2026; c.month = 9; c.day = day
        c.hour = hour; c.minute = minute; c.second = second
        c.timeZone = pacific
        return Calendar(identifier: .gregorian).date(from: c)!
    }

    static let t1 = TripGeometry(
        id: "T1", routeID: "R1", headsign: "North", directionID: 0,
        serviceDate: "20260902", timezone: "America/Los_Angeles",
        route: RouteSummary(shortName: "1", longName: "Straight", color: "0077C0", textColor: "FFFFFF"),
        shape: ShapePayload(lengthM: 1001, points: [[47.6000, -122.3300], [47.6045, -122.3300], [47.6090, -122.3300]]),
        stops: [
            TripStop(id: "ST1", name: "Stop ST1", sequence: 1, lat: 47.6000, lon: -122.3300, alongShapeM: 0, arrivalAt: at(8, 0), departureAt: at(8, 0)),
            TripStop(id: "ST2", name: "Stop ST2", sequence: 2, lat: 47.6045, lon: -122.3300, alongShapeM: 500, arrivalAt: at(8, 5), departureAt: at(8, 5)),
            TripStop(id: "ST3", name: "Stop ST3", sequence: 3, lat: 47.6090, lon: -122.3300, alongShapeM: 1001, arrivalAt: at(8, 10), departureAt: at(8, 10)),
        ],
        thresholds: AdherenceThresholds(maxShapeDistanceM: 60, scheduleEarlyS: 900, scheduleLateS: 5400)
    )

    static func fix(lat: Double, lon: Double, at time: Date, accuracy: Double = 5, speed: Double = 8, course: Double = 0) -> LocationFix {
        LocationFix(latitude: lat, longitude: lon, horizontalAccuracy: accuracy, speed: speed, course: course, timestamp: time)
    }

    /// The catalog's trip payload as the server renders it (spec §4.3).
    static let tripJSON = """
    {"id":"T1","route_id":"R1","headsign":"North","direction_id":0,
     "service_date":"20260902","timezone":"America/Los_Angeles",
     "route":{"short_name":"1","long_name":"Straight","color":"0077C0","text_color":"FFFFFF"},
     "shape":{"length_m":1001.2,"points":[[47.6,-122.33],[47.6045,-122.33],[47.609,-122.33]]},
     "stops":[{"id":"ST1","name":"Stop ST1","sequence":1,"lat":47.6,"lon":-122.33,"along_shape_m":0,
               "arrival_at":"2026-09-02T08:00:00-07:00","departure_at":"2026-09-02T08:00:00-07:00"},
              {"id":"ST3","name":"Stop ST3","sequence":3,"lat":47.609,"lon":-122.33,"along_shape_m":1001.2,
               "arrival_at":"2026-09-03T01:10:00-07:00","departure_at":"2026-09-03T01:10:00-07:00"}],
     "thresholds":{"max_shape_distance_m":60,"schedule_early_s":900,"schedule_late_s":5400}}
    """
}
