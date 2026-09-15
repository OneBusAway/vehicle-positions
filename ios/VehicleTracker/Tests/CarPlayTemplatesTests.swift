import CarPlay
import Testing
@testable import VehicleTracker

@Suite struct CarPlayTemplatesTests {
    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!
    var active: ActiveTrip {
        ActiveTrip(serverTripID: 1, vehicle: Vehicle(id: "bus-1", label: "Bus 1"), trip: TripFixtures.t1,
                   startedAt: TripFixtures.at(7, 55), driverEmail: "d@test.com")
    }

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

    @Test func maneuverVariantsOnTime() {
        // ~445 m along, due 08:04:27 → 33 s late at 08:05, which reads on time.
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6040, lon: -122.3300, at: TripFixtures.at(8, 5)), previous: nil)
        #expect(a.nextStop.id == "ST2")
        let v = CarPlayTemplates.maneuverVariants(stop: a.nextStop, adherence: a, timezone: "America/Los_Angeles")
        #expect(v.count == 3)
        #expect(v[0].hasPrefix("Stop ST2 · 8:05"))
        #expect(v[0].hasSuffix(" · on time"))
        #expect(v[1] == "Stop ST2 · on time")
        #expect(v[2] == "Stop ST2")
    }

    @Test func mapButtonsAreAtMostThree() {
        let buttons = CarPlayTemplates.mapButtons(onPan: {}, onZoomIn: {}, onZoomOut: {})
        let withImages = buttons.filter { $0.image != nil }.count
        let enabled = buttons.filter { $0.isEnabled }.count
        #expect(buttons.count == 3)
        #expect(withImages == 3)
        #expect(enabled == 3)
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

    @Test func vehicleItems() {
        let items = CarPlayTemplates.vehicleItems([Vehicle(id: "bus-1", label: "Bus 1"), Vehicle(id: "bus-2", label: "")], onSelect: { _ in })
        #expect(items.map(\.text) == ["Bus 1", "bus-2"])
    }

    @Test func routeSectionsPutRecentFirst() {
        let routes = [RouteInfo(id: "R1", shortName: "1", longName: "Straight", color: "", textColor: "", type: 3),
                      RouteInfo(id: "R2", shortName: "2", longName: "Loop", color: "", textColor: "", type: 3)]
        let sections = CarPlayTemplates.routeSections(routes, recentIDs: ["R2", "R9"], onSelect: { _ in })
        #expect(sections.map(\.header) == ["Recent", "All routes"])
        #expect(sections[0].items.map(\.text) == ["2"])
        #expect(sections[0].items[0].detailText == "Loop")
        #expect(sections[1].items.map(\.text) == ["1", "2"])
        let none = CarPlayTemplates.routeSections(routes, recentIDs: [], onSelect: { _ in })
        #expect(none.map(\.header) == ["All routes"])
    }

    @Test func tripItemsMarkTheCurrentRun() {
        let page = RouteTripsPage(routeID: "R1", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: [
            TripSummary(id: "a", headsign: "North", directionID: 0, startsAt: TripFixtures.at(7, 0), endsAt: TripFixtures.at(7, 30), firstStop: "A", lastStop: "B"),
            TripSummary(id: "b", headsign: "North", directionID: 0, startsAt: TripFixtures.at(8, 0), endsAt: TripFixtures.at(8, 30), firstStop: "A", lastStop: "B"),
        ])
        let items = CarPlayTemplates.tripItems(page, now: TripFixtures.at(8, 10), onSelect: { _ in })
        #expect(items[0].text?.hasPrefix("7:00") == true)
        #expect(items[0].text?.hasSuffix("→ North") == true)
        #expect(items[0].detailText == "A → B")
        #expect(items[1].detailText == "Now · A → B")
    }

    @Test func listTruncatesToTheCap() {
        let items = (0..<12).map { CPListItem(text: "Route \($0)", detailText: nil) }
        let list = CarPlayTemplates.list(title: "Routes", sections: [(header: nil, items: items)], maxItems: 10)
        #expect(list.itemCount == 10)
        let last = list.sections.last?.items.last as? CPListItem
        #expect(last?.text == "Use iPhone to see more")
        #expect(last?.isEnabled == false)
        let short = CarPlayTemplates.list(title: "Routes", sections: [(header: nil, items: Array(items.prefix(3)))], maxItems: 10)
        #expect(short.itemCount == 3)
    }
}
