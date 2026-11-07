import Foundation
import SwiftUI
import Testing
@testable import VehicleTracker

@Suite struct FormattersTests {
    @Test func distanceText() {
        #expect(Formatters.distance(0) == "0 m")
        #expect(Formatters.distance(449.6) == "450 m")
        #expect(Formatters.distance(1250) == "1.3 km")
    }

    @Test func clockUsesTheAgencyZone() {
        // Locale decides the AM suffix and its spacing; the hour and minute are what matter.
        #expect(Formatters.clock(TripFixtures.at(8, 5), timezone: "America/Los_Angeles").hasPrefix("8:05"))
    }

    /// Kiritimati is UTC+14, so a day formatted in the device's zone would
    /// read as the day before on any machine west of it, CI's UTC included.
    @Test func serviceDayIsFormattedInTheAgencysZone() {
        #expect(Formatters.serviceDay("20261024", timezone: "Pacific/Kiritimati", locale: Locale(identifier: "en_US")) == "Saturday, October 24")
        #expect(Formatters.serviceDay("20260902", timezone: "America/Los_Angeles", locale: Locale(identifier: "en_US")) == "Wednesday, September 2")
    }

    @Test func aServiceDateThatIsNotADateIsShownAsItCame() {
        #expect(Formatters.serviceDay("20261399", timezone: "America/Los_Angeles", locale: Locale(identifier: "en_US")) == "2026-13-99")
        #expect(Formatters.serviceDay("2026102", timezone: "America/Los_Angeles", locale: Locale(identifier: "en_US")) == "2026102")
        #expect(Formatters.serviceDay("20261024", timezone: "Mars/Olympus", locale: Locale(identifier: "en_US")) == "2026-10-24")
        #expect(Formatters.serviceDateLabel("20260922") == "2026-09-22")
        #expect(Formatters.serviceDateLabel("") == "")
    }

    @Test func hexColor() {
        #expect(Color(hex: "0077C0") != nil)
        #expect(Color(hex: "#0077C0") != nil)
        #expect(Color(hex: "") == nil)
        #expect(Color(hex: "zzz") == nil)
    }

    @Test func adherenceTone() {
        let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!
        func tone(_ status: AdherenceStatus, deviation: TimeInterval) -> StatusTone {
            var adherence = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5)), previous: nil)
            adherence.status = status
            adherence.scheduleDeviation = deviation
            return adherence.tone
        }
        #expect(tone(.onTime, deviation: 30) == .onTime)
        #expect(tone(.early, deviation: -120) == .early)
        #expect(tone(.late, deviation: 600) == .late)
        #expect(tone(.offSchedule, deviation: -120) == .early)
        #expect(tone(.offSchedule, deviation: 600) == .late)
        #expect(tone(.offRoute, deviation: 0) == .neutral)
    }
}
