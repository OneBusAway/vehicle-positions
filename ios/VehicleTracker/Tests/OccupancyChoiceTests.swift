import Foundation
import Testing
@testable import VehicleTracker

/// Real `UserDefaults` suites, so a relaunch is a second choice reading the
/// same store.
@Suite struct OccupancyChoiceTests {
    func withDefaults(_ body: (UserDefaults) throws -> Void) rethrows {
        let name = "OccupancyChoiceTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        try body(defaults)
    }

    @Test func aTripStartsWithNone() {
        withDefaults { defaults in
            #expect(OccupancyChoice(defaults: defaults).occupancy(tripID: "T1", serviceDate: "20260902") == nil)
        }
    }

    @Test func aTapSetsItAndAnotherChangesIt() {
        withDefaults { defaults in
            let choice = OccupancyChoice(defaults: defaults)
            choice.choose(.fewSeatsAvailable, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == .fewSeatsAvailable)
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == .full)
        }
    }

    @Test func tappingTheSelectedOneClearsIt() {
        withDefaults { defaults in
            let choice = OccupancyChoice(defaults: defaults)
            choice.choose(.standingRoomOnly, tripID: "T1", serviceDate: "20260902")
            choice.choose(.standingRoomOnly, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == nil)
            #expect(OccupancyChoice(defaults: defaults).occupancy(tripID: "T1", serviceDate: "20260902") == nil,
                    "cleared in the store too, so a relaunch does not bring it back")
        }
    }

    @Test func aNewTripStartsWithNone() {
        withDefaults { defaults in
            let choice = OccupancyChoice(defaults: defaults)
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T2", serviceDate: "20260902") == nil)
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260903") == nil, "the same trip id on another day is another trip")
        }
    }

    @Test func itSurvivesARelaunch() {
        withDefaults { defaults in
            OccupancyChoice(defaults: defaults).choose(.full, tripID: "T1", serviceDate: "20260902")
            #expect(OccupancyChoice(defaults: defaults).occupancy(tripID: "T1", serviceDate: "20260902") == .full)
        }
    }
}
