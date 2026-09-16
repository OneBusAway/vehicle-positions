import Foundation
import Testing
@testable import VehicleTracker

@Suite struct SmokeTests {
    @Test func testTargetLinksTheApp() {
        #expect(Bundle.main.bundleIdentifier == "org.onebusaway.vehicletracker")
    }
}
