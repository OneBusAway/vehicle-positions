import Foundation
import Testing
@testable import VehicleTracker

@Suite struct TripGeometryTests {
    @Test func decodesTheCatalogPayload() throws {
        let trip = try JSONCoding.decoder.decode(TripGeometry.self, from: Data(TripFixtures.tripJSON.utf8))
        #expect(trip.id == "T1")
        #expect(trip.routeID == "R1")
        #expect(trip.directionID == 0)
        #expect(trip.route.shortName == "1")
        #expect(trip.shape.lengthM == 1001.2)
        #expect(trip.shapePoints == [GeoPoint(47.6, -122.33), GeoPoint(47.6045, -122.33), GeoPoint(47.609, -122.33)])
        #expect(!trip.shape.fromStops)
        #expect(trip.stops.count == 2)
        #expect(trip.stops[0].departureAt == TripFixtures.at(8, 0))
        #expect(trip.stops[1].arrivalAt == TripFixtures.at(1, 10, day: 3), "an after-midnight time lands on the next calendar day")
        #expect(trip.thresholds == AdherenceThresholds(maxShapeDistanceM: 60, scheduleEarlyS: 900, scheduleLateS: 5400))
    }

    @Test func aShapeDrawnThroughTheStopsIsReadAsOne() throws {
        let trip = try JSONCoding.decoder.decode(TripGeometry.self, from: Data(Self.throughTheStops(TripFixtures.tripJSON).utf8))
        #expect(trip.shape.fromStops)
    }

    @Test func aServerThatPredatesShapesDrawnThroughTheStopsStillDecodes() throws {
        try #require(TripFixtures.tripJSON.contains(",\"from_stops\":false"), "the fixture no longer writes the mark where this test strips it")
        let json = TripFixtures.tripJSON.replacingOccurrences(of: ",\"from_stops\":false", with: "")
        let trip = try JSONCoding.decoder.decode(TripGeometry.self, from: Data(json.utf8))
        #expect(!trip.shape.fromStops)
    }

    /// The trip in progress is stored with its geometry (`ActiveTrip`), so
    /// one stored by a build before the mark has to read back too, with a
    /// shape from the feed.
    @Test func aTripStoredBeforeShapesDrawnThroughTheStopsWereMarkedStillLoads() throws {
        var marked = TripFixtures.t1
        marked.shape.fromStops = true
        let stored = try JSONCoding.encoder.encode(marked)
        #expect(try JSONCoding.decoder.decode(TripGeometry.self, from: stored) == marked, "the mark is kept with the trip")

        var json = try #require(try JSONSerialization.jsonObject(with: stored) as? [String: Any])
        var shape = try #require(json["shape"] as? [String: Any])
        #expect(shape.removeValue(forKey: "from_stops") as? Bool == true)
        json["shape"] = shape
        let older = try JSONSerialization.data(withJSONObject: json)

        #expect(try JSONCoding.decoder.decode(TripGeometry.self, from: older) == TripFixtures.t1)
    }

    /// `shapes.txt` is optional, and the server draws a trip that has none as
    /// straight lines through its stops, marked `shape.from_stops`. The road
    /// runs where it runs between two stops, so distance from those lines is
    /// not off route.
    @Test func aFixFarFromAShapeDrawnThroughTheStopsIsNotOffRoute() throws {
        // The two stops ten minutes apart, so halfway along is due at 08:05.
        let payload = TripFixtures.tripJSON.replacingOccurrences(of: "2026-09-03T01:10:00-07:00", with: "2026-09-02T08:10:00-07:00")
        // 150 m east of the line, halfway along it.
        let offTheLine = TripFixtures.fix(lat: 47.6045, lon: -122.3280, at: TripFixtures.at(8, 5))
        func judged(on payload: String) throws -> Adherence {
            let trip = try JSONCoding.decoder.decode(TripGeometry.self, from: Data(payload.utf8))
            return try #require(AdherenceEvaluator(trip: trip)).evaluate(offTheLine, previous: nil)
        }

        let fromTheFeed = try judged(on: payload)
        let throughTheStops = try judged(on: Self.throughTheStops(payload))

        #expect(abs(throughTheStops.projection.distanceToShape - 150) <= 3)
        #expect(fromTheFeed.status == .offRoute)
        #expect(throughTheStops.isOnRoute)
        #expect(throughTheStops.status == .onTime, "judged on the schedule instead")
    }

    /// `payload` as the server sends a trip whose shape it drew through the stops.
    static func throughTheStops(_ payload: String) -> String {
        precondition(payload.contains("\"from_stops\":false"), "the fixture no longer writes the mark where this test sets it")
        return payload.replacingOccurrences(of: "\"from_stops\":false", with: "\"from_stops\":true")
    }

    @Test func nullDirectionDecodesAsNil() throws {
        let json = TripFixtures.tripJSON.replacingOccurrences(of: "\"direction_id\":0", with: "\"direction_id\":null")
        let trip = try JSONCoding.decoder.decode(TripGeometry.self, from: Data(json.utf8))
        #expect(trip.directionID == nil)
    }

    /// The feed is not guaranteed to hand back well-formed pairs, and a
    /// three-element "point" must not shift every later coordinate.
    @Test func malformedShapePairsAreDropped() {
        var trip = TripFixtures.t1
        trip.shape = ShapePayload(lengthM: 1001, points: [[1, 2], [3], [4, 5, 6], [7, 8]])
        #expect(trip.shapePoints == [GeoPoint(1, 2), GeoPoint(7, 8)])
    }

    @Test func roundTripsThroughTheEncoder() throws {
        let data = try JSONCoding.encoder.encode(TripFixtures.t1)
        let back = try JSONCoding.decoder.decode(TripGeometry.self, from: data)
        #expect(back == TripFixtures.t1)
    }

    @Test func scheduleInterpolation() {
        let stops = TripFixtures.t1.stops
        #expect(ScheduleInterpolator.scheduledTime(at: -10, stops: stops) == TripFixtures.at(8, 0))
        let mid = ScheduleInterpolator.scheduledTime(at: 250, stops: stops)!
        #expect(abs(mid.timeIntervalSince(TripFixtures.at(8, 2, 30))) <= 3)
        #expect(ScheduleInterpolator.scheduledTime(at: 5000, stops: stops) == TripFixtures.at(8, 10))
        #expect(ScheduleInterpolator.scheduledTime(at: 100, stops: []) == nil)
    }
}
