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

    @Test func locationAccessProblemsAreNamed() {
        #expect(TripSession.ReportingStatus.locationDenied.label == "Location off · Tap to fix")
        #expect(TripSession.ReportingStatus.preciseLocationOff.label == "Precise Location off · Tap to fix")
    }
}
