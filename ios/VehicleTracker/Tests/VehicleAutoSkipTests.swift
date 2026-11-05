import Foundation
import Testing
@testable import VehicleTracker

@Suite struct VehicleAutoSkipTests {
    let only = Vehicle(id: "bus-1", label: "Bus 1")

    @Test func oneVehicleIsSkippedToOnTheFirstLoad() {
        #expect(VehicleAutoSkip.vehicle(toSkipTo: [only], alreadySkipped: false, pathIsEmpty: true) == only)
    }

    /// Back from the routes loads the vehicle screen again with nothing
    /// pushed; skipping there is the loop that hid Sign out.
    @Test func notAgainOnceThisVisitHasSkipped() {
        #expect(VehicleAutoSkip.vehicle(toSkipTo: [only], alreadySkipped: true, pathIsEmpty: true) == nil)
    }

    @Test(arguments: [
        ([Vehicle(id: "bus-1", label: "Bus 1"), Vehicle(id: "bus-2", label: "Bus 2")], true),
        ([Vehicle(id: "bus-1", label: "Bus 1")], false),
        ([], true),
    ] as [([Vehicle], Bool)])
    func noSkipWithAChoiceAScreenPushedOrNoVehicle(vehicles: [Vehicle], pathIsEmpty: Bool) {
        #expect(VehicleAutoSkip.vehicle(toSkipTo: vehicles, alreadySkipped: false, pathIsEmpty: pathIsEmpty) == nil)
    }
}
