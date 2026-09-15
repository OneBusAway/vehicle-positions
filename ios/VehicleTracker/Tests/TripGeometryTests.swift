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
        #expect(trip.stops.count == 2)
        #expect(trip.stops[0].departureAt == TripFixtures.at(8, 0))
        #expect(trip.stops[1].arrivalAt == TripFixtures.at(1, 10, day: 3), "an after-midnight time lands on the next calendar day")
        #expect(trip.thresholds == AdherenceThresholds(maxShapeDistanceM: 60, scheduleEarlyS: 900, scheduleLateS: 5400))
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
