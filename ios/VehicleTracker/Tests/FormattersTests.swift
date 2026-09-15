import Foundation
import SwiftUI
import Testing
@testable import VehicleTracker

@Suite struct FormattersTests {
    @Test func deviationText() {
        #expect(Formatters.deviation(0) == "on time")
        #expect(Formatters.deviation(59) == "on time")
        #expect(Formatters.deviation(-59) == "on time")
        #expect(Formatters.deviation(60) == "1 min late")
        #expect(Formatters.deviation(200) == "3 min late")
        #expect(Formatters.deviation(-90) == "2 min early")
        #expect(Formatters.deviation(3600) == "60 min late")
    }

    @Test func distanceText() {
        #expect(Formatters.distance(0) == "0 m")
        #expect(Formatters.distance(449.6) == "450 m")
        #expect(Formatters.distance(1250) == "1.3 km")
    }

    @Test func elapsedText() {
        #expect(Formatters.elapsed(0) == "0:00")
        #expect(Formatters.elapsed(65) == "1:05")
        #expect(Formatters.elapsed(3725) == "1:02:05")
    }

    @Test func clockUsesTheAgencyZone() {
        // Locale decides the AM suffix and its spacing; the hour and minute are what matter.
        #expect(Formatters.clock(TripFixtures.at(8, 5), timezone: "America/Los_Angeles").hasPrefix("8:05"))
    }

    @Test func hexColor() {
        #expect(Color(hex: "0077C0") != nil)
        #expect(Color(hex: "#0077C0") != nil)
        #expect(Color(hex: "") == nil)
        #expect(Color(hex: "zzz") == nil)
    }
}
