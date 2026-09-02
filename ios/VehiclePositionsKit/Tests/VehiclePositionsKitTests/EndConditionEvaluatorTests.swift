import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct EndConditionEvaluatorTests {
    let config = RideReporterConfiguration(serverURL: URL(string: "https://x")!, appID: "a", appVersion: "1")
    let dest = Coordinate(latitude: 47.6090, longitude: -122.3300) // 1 km north of the start
    func sample(lat: Double, lon: Double = -122.33, stationary: Bool = false, at t: TimeInterval = 0) -> LocationSample {
        LocationSample(fix: LocationFix(latitude: lat, longitude: lon, horizontalAccuracy: 5, speed: 8, course: 0, timestamp: Date(timeIntervalSince1970: t)), isStationary: stationary)
    }

    @Test func arrivalRequiresTravelGuard() {
        var e = EndConditionEvaluator(configuration: config, destination: dest)
        let now = Date()
        #expect(e.evaluate(sample(lat: 47.6089), now: now) == nil, "starting next to the destination does not end")
        #expect(e.evaluate(sample(lat: 47.6000), now: now) == nil)
        #expect(e.evaluate(sample(lat: 47.6089), now: now) == nil, "first fix was near the destination: travelled distance from the first fix is ~0")

        var f = EndConditionEvaluator(configuration: config, destination: dest)
        #expect(f.evaluate(sample(lat: 47.6000), now: now) == nil)
        #expect(f.evaluate(sample(lat: 47.6050), now: now) == nil, "500 m away")
        #expect(f.evaluate(sample(lat: 47.6086), now: now) == .arrived, "≈45 m from the destination after 950 m of travel")
    }

    @Test func noDestinationNeverArrives() {
        var e = EndConditionEvaluator(configuration: config, destination: nil)
        #expect(e.evaluate(sample(lat: 47.6000), now: Date()) == nil)
        #expect(e.evaluate(sample(lat: 47.6090), now: Date()) == nil)
    }

    @Test func stationaryTimeout() {
        var e = EndConditionEvaluator(configuration: config, destination: nil)
        let t0 = Date(timeIntervalSince1970: 0)
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0) == nil)
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0.addingTimeInterval(599)) == nil)
        #expect(e.evaluate(sample(lat: 47.6, stationary: false), now: t0.addingTimeInterval(600)) == nil, "movement resets")
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0.addingTimeInterval(700)) == nil)
        #expect(e.evaluate(sample(lat: 47.6, stationary: true), now: t0.addingTimeInterval(1300)) == .stationary)
    }

    @Test func diagnostics() {
        var e = EndConditionEvaluator(configuration: config, destination: nil)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .accuracyLimited), now: Date()) == nil)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .insufficientlyInUse), now: Date()) == nil)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .locationUnavailable), now: Date()) == .locationUnavailable)
        #expect(e.evaluate(LocationSample(fix: nil, diagnostic: .authorizationDenied), now: Date()) == .authorizationDenied)
    }

    @Test func geoDistance() {
        let d = GeoDistance.meters(from: Coordinate(latitude: 47.6000, longitude: -122.33), to: dest)
        #expect(abs(d - 1001) < 5)
    }
}
