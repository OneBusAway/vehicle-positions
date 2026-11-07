import Testing
@testable import VehicleTracker

@Suite struct PickerTextTests {
    func route(_ shortName: String, _ longName: String) -> RouteInfo {
        RouteInfo(id: "R", shortName: shortName, longName: longName, color: "", textColor: "", type: 3)
    }

    @Test func aRouteRowIsReadAsTheNumberThenTheName() {
        #expect(PickerText.routeRow(route("7", "Rainier Beach")) == "Route 7, Rainier Beach")
    }

    /// A feed may name a route without numbering it, or the other way round.
    @Test func aRouteWithOneNameIsReadOnce() {
        #expect(PickerText.routeRow(route("", "Loop")) == "Route Loop")
        #expect(PickerText.routeRow(route("7", "")) == "Route 7")
    }
}
