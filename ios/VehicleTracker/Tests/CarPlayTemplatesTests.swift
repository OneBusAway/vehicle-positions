import CarPlay
import Testing
@testable import VehicleTracker

@Suite struct CarPlayTemplatesTests {
    @Test func titleButtonIsDisabledText() {
        let b = CarPlayTemplates.titleButton("Sign in on iPhone")
        #expect(b.title == "Sign in on iPhone")
        #expect(!b.isEnabled)
    }

    @Test func signedOutHasNoStartButton() {
        let (leading, trailing) = CarPlayTemplates.idleBarButtons(phase: .signedOut, onStart: {})
        #expect(leading.map(\.title) == ["Sign in on iPhone"])
        #expect(trailing.isEmpty)
    }

    @Test func idleOffersStart() {
        let (leading, trailing) = CarPlayTemplates.idleBarButtons(phase: .idle, onStart: {})
        #expect(leading.map(\.title) == ["OBA Vehicle Tracker"])
        #expect(trailing.map(\.title) == ["Start trip"])
        #expect(trailing[0].isEnabled)
    }

    @Test func startingShowsProgressAndNoStart() {
        let (leading, trailing) = CarPlayTemplates.idleBarButtons(phase: .starting, onStart: {})
        #expect(leading.map(\.title) == ["Starting…"])
        #expect(trailing.isEmpty)
    }

    @Test func pausedOffersEndAndResumeHint() {
        let (leading, trailing) = CarPlayTemplates.pausedBarButtons(onEnd: {})
        #expect(leading.map(\.title) == ["End"])
        #expect(leading[0].isEnabled)
        #expect(trailing.map(\.title) == ["Resume on iPhone"])
        #expect(!trailing[0].isEnabled)
    }

    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!
    var active: ActiveTrip {
        ActiveTrip(serverTripID: 1, vehicle: Vehicle(id: "bus-1", label: "Bus 1"), trip: TripFixtures.t1,
                   startedAt: TripFixtures.at(7, 55), driverEmail: "d@test.com")
    }

    @Test func maneuverVariantsLongestFirst() {
        // ~300 m along, due 08:03:00 → 2 min late at 08:05.
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6027, lon: -122.3300, at: TripFixtures.at(8, 5)), previous: nil)
        let v = CarPlayTemplates.maneuverVariants(stop: a.nextStop, adherence: a, timezone: "America/Los_Angeles")
        #expect(v.count == 3)
        #expect(v[0].hasPrefix("Stop ST2 · 8:05"))
        #expect(v[0].hasSuffix("2 min late"))
        #expect(v[1] == "Stop ST2 · 2 min late")
        #expect(v[2] == "Stop ST2")
        let m = CarPlayTemplates.maneuver(stop: a.nextStop, adherence: a, timezone: "America/Los_Angeles")
        #expect(m.instructionVariants == v)
        #expect(m.symbolImage != nil)
    }

    @Test func maneuverWithoutAdherenceShowsScheduleOnly() {
        let v = CarPlayTemplates.maneuverVariants(stop: TripFixtures.t1.stops[0], adherence: nil, timezone: "America/Los_Angeles")
        #expect(v.count == 2)
        #expect(v[0].hasPrefix("Stop ST1 · 8:00"))
        #expect(v[1] == "Stop ST1")
    }

    @Test func timeRemainingColours() {
        func a(_ seconds: Double) -> Adherence {
            evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5).addingTimeInterval(seconds)), previous: nil)
        }
        #expect(CarPlayTemplates.timeRemainingColor(for: a(0)) == .green)
        #expect(CarPlayTemplates.timeRemainingColor(for: a(400)) == .orange)
        #expect(CarPlayTemplates.timeRemainingColor(for: a(-120)) == .red)
        #expect(CarPlayTemplates.timeRemainingColor(for: a(6000)) == .default)
        let off = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3200, at: TripFixtures.at(8, 5)), previous: nil)
        #expect(CarPlayTemplates.timeRemainingColor(for: off) == .default)
        #expect(CarPlayTemplates.timeRemainingColor(for: nil) == .default)
    }

    @Test func estimates() {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5), speed: 10), previous: nil)
        let stop = CarPlayTemplates.stopEstimates(adherence: a)
        #expect(abs(stop.distanceRemaining.converted(to: .meters).value - 501) <= 5)
        #expect(abs(stop.timeRemaining - 50.1) <= 1)
        let trip = CarPlayTemplates.tripEstimates(active: active, adherence: a)
        #expect(abs(trip.distanceRemaining.converted(to: .meters).value - 501) <= 5)
    }

    @Test func detailItems() {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5), accuracy: 7), previous: nil)
        let items = CarPlayTemplates.detailItems(active: active, adherence: a, reporting: .connected(fixesSent: 214))
        #expect(items.count == 6)
        #expect(items.map(\.title) == ["Route", "Trip", "Schedule", "Next stop", "Reporting", "GPS"])
        #expect(items[0].detail == "1 · Straight")
        #expect(items[1].detail?.hasPrefix("North · 8:00") == true)
        #expect(items[2].detail == "on time")
        #expect(items[3].detail?.hasPrefix("Stop ST3 · 8:10") == true)
        #expect(items[4].detail == "Connected · 214 sent")
        #expect(items[5].detail == "±7 m")
        let waiting = CarPlayTemplates.detailItems(active: active, adherence: nil, reporting: .noGPS)
        #expect(waiting[2].detail == "Waiting for GPS")
        #expect(waiting[4].detail == "No GPS")
    }

    @Test func offRouteAlertNeverTimesOut() {
        let off = evaluator.evaluate(TripFixtures.fix(lat: 47.60225, lon: -122.3290, at: TripFixtures.at(8, 2, 30)), previous: nil)
        let alert = CarPlayTemplates.offRouteAlert(adherence: off, onOK: {})
        #expect(alert.titleVariants == ["Off route"])
        #expect(alert.subtitleVariants.first?.hasPrefix("75 m") == true)
        #expect(alert.duration == 0)
        #expect(alert.primaryAction.title == "OK")
    }
}
