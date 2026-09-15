import Foundation
import Testing
@testable import VehicleTracker

@Suite struct AdherenceEvaluatorTests {
    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!

    @Test func onTimeAtAStop() {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5)), previous: nil)
        #expect(a.isOnRoute)
        #expect(abs(a.projection.alongShape - 500) <= 3)
        #expect(abs(a.scheduleDeviation) <= 3)
        #expect(a.status == .onTime)
        #expect(a.nextStop.id == "ST3")
        #expect(abs(a.distanceToNextStop - 501) <= 5)
        #expect(abs(a.timeToNextStop - 501 / 8) <= 1)
    }

    @Test func lateAndEarlyThresholds() {
        let stop2 = GeoPoint(47.6045, -122.3300)
        func status(secondsLate: Double) -> AdherenceStatus {
            evaluator.evaluate(TripFixtures.fix(lat: stop2.lat, lon: stop2.lon, at: TripFixtures.at(8, 5).addingTimeInterval(secondsLate)), previous: nil).status
        }
        #expect(status(secondsLate: 240) == .onTime, "4 min late is still on time")
        #expect(status(secondsLate: 301) == .late)
        #expect(status(secondsLate: -59) == .onTime)
        #expect(status(secondsLate: -61) == .early)
        #expect(status(secondsLate: 5401) == .offSchedule, "beyond the server's late window")
        #expect(status(secondsLate: -901) == .offSchedule, "beyond the server's early window")
    }

    @Test func offRouteUsesThresholdPlusAccuracy() {
        // ~75 m east of the line: beyond 60 m with 5 m accuracy, within it with 20 m accuracy.
        let coarse = evaluator.evaluate(TripFixtures.fix(lat: 47.60225, lon: -122.3290, at: TripFixtures.at(8, 2, 30), accuracy: 20), previous: nil)
        #expect(coarse.isOnRoute)
        #expect(coarse.status == .onTime)
        let precise = evaluator.evaluate(TripFixtures.fix(lat: 47.60225, lon: -122.3290, at: TripFixtures.at(8, 2, 30), accuracy: 5), previous: nil)
        #expect(!precise.isOnRoute)
        #expect(precise.status == .offRoute)
        #expect(abs(precise.projection.distanceToShape - 75) <= 3)
    }

    @Test func unknownAccuracyAddsNothing() {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.60225, lon: -122.3290, at: TripFixtures.at(8, 2, 30), accuracy: -1), previous: nil)
        #expect(!a.isOnRoute)
    }

    @Test func slowOrUnknownSpeedUsesTheFloor() {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5), speed: -1), previous: nil)
        #expect(abs(a.timeToNextStop - 501 / 3) <= 1)
    }

    @Test func lastStopIsNextBeyondTheEnd() {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6095, lon: -122.3300, at: TripFixtures.at(8, 11)), previous: nil)
        #expect(a.nextStop.id == "ST3")
        // The fixture's stop is declared at 1001 m but the shape's own
        // haversine length over its three points is ~1000.75 m, so the
        // projection clamps just short of the declared stop distance.
        #expect(a.distanceToNextStop <= 1)
    }

    @Test func previousOnRouteFixSuppliesTheHint() {
        // A loop where only the hint separates the first and last pass.
        let loop = TripGeometry(
            id: "L", routeID: "R2", headsign: "Loop", directionID: nil, serviceDate: "20260902", timezone: "America/Los_Angeles",
            route: RouteSummary(shortName: "2", longName: "Loop", color: "", textColor: ""),
            shape: ShapePayload(lengthM: 2000, points: [[47.6000, -122.3300], [47.6045, -122.3300], [47.6045, -122.3234], [47.6000, -122.3234], [47.6000, -122.3300]]),
            stops: [
                TripStop(id: "A", name: "A", sequence: 1, lat: 47.6000, lon: -122.3300, alongShapeM: 0, arrivalAt: TripFixtures.at(9, 0), departureAt: TripFixtures.at(9, 0)),
                TripStop(id: "B", name: "B", sequence: 2, lat: 47.6000, lon: -122.3300, alongShapeM: 1995, arrivalAt: TripFixtures.at(9, 20), departureAt: TripFixtures.at(9, 20)),
            ],
            thresholds: TripFixtures.t1.thresholds)
        let ev = AdherenceEvaluator(trip: loop)!
        let lateInLoop = ev.evaluate(TripFixtures.fix(lat: 47.6001, lon: -122.3292, at: TripFixtures.at(9, 15)), previous: nil) // ~435 m along the south leg
        #expect(lateInLoop.projection.alongShape > 1400)
        let atShared = ev.evaluate(TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(9, 20)), previous: lateInLoop)
        #expect(atShared.projection.alongShape > ev.shape.length - 60, "the hint picks the last pass")
        let unhinted = ev.evaluate(TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(9, 20)), previous: nil)
        #expect(unhinted.projection.alongShape < 1)
    }

    @Test func offRoutePreviousDoesNotHint() {
        let off = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3200, at: TripFixtures.at(8, 5)), previous: nil)
        #expect(!off.isOnRoute)
        let back = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5, 30)), previous: off)
        #expect(back.isOnRoute)
        #expect(abs(back.projection.alongShape - 500) <= 3)
    }

    @Test func rejectsATripWithoutAShapeOrStops() {
        var noShape = TripFixtures.t1
        noShape.shape = ShapePayload(lengthM: 0, points: [[47.6, -122.33]])
        #expect(AdherenceEvaluator(trip: noShape) == nil)
        var noStops = TripFixtures.t1
        noStops.stops = []
        #expect(AdherenceEvaluator(trip: noStops) == nil)
    }
}
