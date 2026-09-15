import Foundation
import Testing
@testable import VehicleTracker

@Suite struct TripRunTests {
    func trip(_ id: String, _ start: Date, _ end: Date) -> TripSummary {
        TripSummary(id: id, headsign: "H", directionID: nil, startsAt: start, endsAt: end, firstStop: "A", lastStop: "B")
    }

    @Test func picksTheRunContainingNow() {
        let trips = [trip("a", TripFixtures.at(7, 0), TripFixtures.at(7, 30)), trip("b", TripFixtures.at(8, 0), TripFixtures.at(8, 30)), trip("c", TripFixtures.at(9, 0), TripFixtures.at(9, 30))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 10))?.id == "b")
    }

    @Test func otherwisePicksTheNextToStart() {
        let trips = [trip("a", TripFixtures.at(7, 0), TripFixtures.at(7, 30)), trip("c", TripFixtures.at(9, 0), TripFixtures.at(9, 30))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 10))?.id == "c")
    }

    @Test func nilWhenEveryRunIsOver() {
        let trips = [trip("a", TripFixtures.at(7, 0), TripFixtures.at(7, 30))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 10)) == nil)
        #expect(TripRun.highlighted(in: [], now: TripFixtures.at(8, 10)) == nil)
    }
}
