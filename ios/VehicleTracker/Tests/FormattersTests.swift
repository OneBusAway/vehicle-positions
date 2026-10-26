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

    /// `reporting` starts out `.connected`, so a trip a relaunch found paused
    /// would read green "Reporting" if the banner went by the status alone.
    @Test func bannerReadsThePhaseBeforeTheStatus() {
        let active = ActiveTrip(serverTripID: 1, vehicle: Vehicle(id: "bus-1", label: "Bus 1"),
                                trip: TripFixtures.t1, startedAt: TripFixtures.at(8, 0),
                                driverEmail: "d@test.com")

        #expect(TrackingBanner.text(phase: .paused(active), reporting: .connected) == "Paused — not reporting")
        #expect(TrackingBanner.tone(phase: .paused(active), reporting: .connected) == .problem)

        #expect(TrackingBanner.text(phase: .ending(active), reporting: .connected) == "Ending…")
        #expect(TrackingBanner.tone(phase: .ending(active), reporting: .connected) == .neutral)

        #expect(TrackingBanner.text(phase: .active(active), reporting: .connected) == "Reporting")
        #expect(TrackingBanner.tone(phase: .active(active), reporting: .connected) == .reporting)

        #expect(TrackingBanner.text(phase: .active(active), reporting: .noNetwork) == "No connection")
        #expect(TrackingBanner.tone(phase: .active(active), reporting: .noNetwork) == .problem)

        #expect(TrackingBanner.text(phase: .active(active), reporting: .refused) == "Reports refused — ask your dispatcher")
        #expect(TrackingBanner.tone(phase: .active(active), reporting: .refused) == .problem)
    }

    /// Problems are ink and signed out has its own card, as on Android: red
    /// means only early.
    @Test func bannerToneForEveryReportingStatus() {
        let active = ActiveTrip(serverTripID: 1, vehicle: Vehicle(id: "bus-1", label: "Bus 1"),
                                trip: TripFixtures.t1, startedAt: TripFixtures.at(8, 0),
                                driverEmail: "d@test.com")
        let expected: [(TripSession.ReportingStatus, BannerTone)] = [
            (.connected, .reporting),
            (.noNetwork, .problem),
            (.noGPS, .problem),
            (.authExpired, .signedOut),
            (.clockSkew, .problem),
            (.refused, .problem),
            (.needsForeground, .problem),
            (.locationLost, .problem),
        ]
        for (reporting, tone) in expected {
            #expect(TrackingBanner.tone(phase: .active(active), reporting: reporting) == tone, "\(reporting)")
            // The phase speaks first, whatever the status says.
            #expect(TrackingBanner.tone(phase: .paused(active), reporting: reporting) == .problem, "\(reporting)")
            #expect(TrackingBanner.tone(phase: .ending(active), reporting: reporting) == .neutral, "\(reporting)")
        }
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
