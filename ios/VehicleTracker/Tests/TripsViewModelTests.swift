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

    @Test func onAFrequentRoutePicksTheRunStartingNearestNow() {
        // Six runs are under way at 14:03; the earliest of them, 13:10, is
        // nearly over. The driver is about to drive the 14:00.
        #expect(TripRun.highlighted(in: TripFixtures.frequentService, now: TripFixtures.at(14, 3))?.id == "14:00")
        #expect(TripRun.highlighted(in: TripFixtures.frequentService, now: TripFixtures.at(14, 7))?.id == "14:10")
    }

    @Test func aTieGoesToTheLaterStart() {
        let trips = [trip("a", TripFixtures.at(8, 0), TripFixtures.at(8, 30)), trip("b", TripFixtures.at(9, 0), TripFixtures.at(9, 30))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 30))?.id == "b")
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 29))?.id == "a")
    }

    @Test func skipsARunThatHasEndedEvenWithTheNearestStart() {
        let trips = [trip("a", TripFixtures.at(8, 0), TripFixtures.at(8, 5)), trip("b", TripFixtures.at(8, 20), TripFixtures.at(8, 40))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 7))?.id == "b")
    }

    @Test func aRunEndingExactlyNowStillCounts() {
        let trips = [trip("a", TripFixtures.at(8, 0), TripFixtures.at(8, 30))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 30))?.id == "a")
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(8, 30, 1)) == nil)
    }

    @Test func onASparseRouteKeepsTheRunUnderWay() {
        let trips = [trip("a", TripFixtures.at(6, 0), TripFixtures.at(10, 0)), trip("b", TripFixtures.at(14, 0), TripFixtures.at(15, 0))]
        #expect(TripRun.highlighted(in: trips, now: TripFixtures.at(9, 0))?.id == "a")
    }

    @Test func opensAtTheRunJustBeforeTheHighlightedOne() {
        let trips = [trip("a", TripFixtures.at(7, 0), TripFixtures.at(7, 30)), trip("b", TripFixtures.at(8, 0), TripFixtures.at(8, 30)), trip("c", TripFixtures.at(9, 0), TripFixtures.at(9, 30))]
        #expect(TripRun.openAt(in: trips, now: TripFixtures.at(8, 10)) == "a")
        #expect(TripRun.openAt(in: TripFixtures.frequentService, now: TripFixtures.at(14, 3)) == "13:50")
    }

    @Test func opensAtTheHighlightedRunWhenItIsTheFirst() {
        let trips = [trip("a", TripFixtures.at(7, 0), TripFixtures.at(7, 30)), trip("b", TripFixtures.at(8, 0), TripFixtures.at(8, 30))]
        #expect(TripRun.openAt(in: trips, now: TripFixtures.at(6, 0)) == "a")
    }

    @Test func opensAtTheLastRunOnceEveryRunIsOver() {
        let trips = [trip("a", TripFixtures.at(7, 0), TripFixtures.at(7, 30)), trip("b", TripFixtures.at(8, 0), TripFixtures.at(8, 30))]
        #expect(TripRun.openAt(in: trips, now: TripFixtures.at(9, 0)) == "b")
        #expect(TripRun.openAt(in: [], now: TripFixtures.at(9, 0)) == nil)
    }
}
