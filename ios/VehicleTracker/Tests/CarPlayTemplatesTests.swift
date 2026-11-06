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

    /// The status card's answer, as the phone shows it, on the next stop's card.
    func status(_ adherence: Adherence?, reporting: TripSession.ReportingStatus = .connected,
                phase: TripSession.Phase? = nil) throws -> TripStatus {
        try #require(TripStatus(phase: phase ?? .active(active), latest: adherence, reporting: reporting))
    }

    @Test func maneuverVariantsLongestFirst() {
        // ~300 m along, heading for ST2 at 08:05.
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6027, lon: -122.3300, at: TripFixtures.at(8, 5)), previous: nil)
        let v = CarPlayTemplates.maneuverVariants(stop: a.nextStop, answer: "6 min late", timezone: "America/Los_Angeles")
        #expect(v.count == 3)
        #expect(v[0].hasPrefix("Stop ST2 · 8:05"))
        #expect(v[0].hasSuffix(" · 6 min late"))
        #expect(v[1] == "Stop ST2 · 6 min late")
        #expect(v[2] == "Stop ST2")
        let m = CarPlayTemplates.maneuver(stop: a.nextStop, answer: "6 min late", timezone: "America/Los_Angeles")
        #expect(m.instructionVariants == v)
        #expect(m.symbolImage != nil)
    }

    /// Three minutes behind is inside the on-time window, so the car says
    /// On time, as the phone's card does.
    @Test func maneuverVariantsSayOnTimeWheneverThePhoneDoes() throws {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(8, 3)), previous: nil)
        #expect(a.nextStop.id == "ST2")
        let v = CarPlayTemplates.maneuverVariants(stop: a.nextStop, answer: try status(a).card.answer, timezone: "America/Los_Angeles")
        #expect(v.count == 3)
        #expect(v[0].hasPrefix("Stop ST2 · 8:05"))
        #expect(v[0].hasSuffix(" · On time"))
        #expect(v[1] == "Stop ST2 · On time")
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

    @Test func maneuverWithoutAnAnswerShowsScheduleOnly() {
        let v = CarPlayTemplates.maneuverVariants(stop: TripFixtures.t1.stops[0], answer: nil, timezone: "America/Los_Angeles")
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

    @Test func detailItems() throws {
        let a = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5), accuracy: 7), previous: nil)
        let items = CarPlayTemplates.detailItems(status: try status(a), adherence: a, fixesSent: 214)
        #expect(items.count == 6)
        #expect(items.map(\.title) == ["Route", "Trip", "Schedule", "Next stop", "Reports", "GPS"])
        #expect(items[0].detail == "1 · Straight")
        #expect(items[1].detail?.hasPrefix("North · 8:00") == true)
        #expect(items[2].detail == "On time")
        #expect(items[3].detail?.hasPrefix("Stop ST3 · 8:10") == true)
        #expect(items[3].detail?.hasSuffix(" m") == true)
        #expect(items[4].detail == "Reporting · 214 sent")
        #expect(items[5].detail == "±7 m")
    }

    /// Off schedule, the Schedule row keeps the words main showed with the
    /// deviation, and the Next stop row stays a stop with its time and
    /// distance: the card's question never lands under "Next stop".
    @Test func detailItemsKeepOffScheduleInTheScheduleRow() throws {
        let early = evaluator.evaluate(TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(8, 0).addingTimeInterval(-1260)), previous: nil)
        try #require(early.status == .offSchedule)
        let items = CarPlayTemplates.detailItems(status: try status(early), adherence: early, fixesSent: 1)
        #expect(items[2].detail == "Off schedule · 21 min early")
        #expect(items[3].detail?.hasPrefix("Stop ST2 · 8:05") == true)
        #expect(items[3].detail?.hasSuffix(" m") == true)
        #expect(items[3].detail?.contains("right run") == false)
    }

    /// The Reports row says what the phone's problem line says, from the
    /// same judgement: a paused trip reads Location stopped though its
    /// reporting status is still `.connected`.
    @Test func detailItemsCarryThePhonesProblemLine() throws {
        let waiting = CarPlayTemplates.detailItems(status: try status(nil, reporting: .noGPS), adherence: nil, fixesSent: 0)
        #expect(waiting[2].detail == "Waiting for GPS")
        #expect(waiting[3].detail == "—", "no next stop before the first fix, as on main")
        #expect(waiting[4].detail == "No GPS signal · Position paused")
        #expect(waiting[5].detail == "—")

        let offline = CarPlayTemplates.detailItems(status: try status(nil, reporting: .noNetwork), adherence: nil, fixesSent: 3)
        #expect(offline[4].detail == "No connection · Riders can't see you")

        let paused = CarPlayTemplates.detailItems(status: try status(nil, phase: .paused(active)), adherence: nil, fixesSent: 3)
        #expect(paused[4].detail == "Location stopped · Tap Resume")

        let signedOut = CarPlayTemplates.detailItems(status: try status(nil, reporting: .authExpired), adherence: nil, fixesSent: 3)
        #expect(signedOut[4].detail == "Reports stopped · Sign in on iPhone")

        let ending = CarPlayTemplates.detailItems(status: try status(nil, phase: .ending(active)), adherence: nil, fixesSent: 3)
        #expect(ending[4].detail == "Ending… · 3 sent", "a paused trip being ended is not Reporting")
    }

    @Test func offRouteAlertSaysHowFarAndNeverTimesOut() throws {
        var off = evaluator.evaluate(TripFixtures.fix(lat: 47.60225, lon: -122.3290, at: TripFixtures.at(8, 2, 30)), previous: nil)
        try #require(off.status == .offRoute)
        off.projection.distanceToShape = 98
        let alert = CarPlayTemplates.offRouteAlert(adherence: off)
        #expect(alert.titleVariants == ["Off route · 98 m"])
        #expect(alert.subtitleVariants.isEmpty)
        #expect(alert.duration == 0)
        #expect(alert.primaryAction.title == "OK")
        #expect(CarPlayTemplates.offRouteTitle(adherence: off) == "Off route · 98 m")
    }

    @Test func theEndAlertsUseThePhonesWords() {
        var ended = false, kept = false
        let end = CarPlayTemplates.endTripAlert(onEnd: { ended = true }, onKeepGoing: { kept = true })
        #expect(end.titleVariants == ["End this trip?"])
        #expect(end.actions.map(\.title) == ["End trip", "Keep going"])
        #expect(end.actions.map(\.style) == [.destructive, .cancel])
        end.actions[0].handler(end.actions[0])
        #expect(ended && !kept)
        end.actions[1].handler(end.actions[1])
        #expect(kept)

        var tried = false, local = false, going = false
        let notEnded = CarPlayTemplates.notEndedAlert(onTryAgain: { tried = true }, onEndOnThisPhone: { local = true }, onKeepGoing: { going = true })
        #expect(notEnded.titleVariants == ["Trip not ended yet"])
        #expect(notEnded.actions.map(\.title) == ["Try again", "End on this phone", "Keep going"])
        #expect(notEnded.actions.map(\.style) == [.default, .destructive, .cancel])
        notEnded.actions[0].handler(notEnded.actions[0])
        notEnded.actions[1].handler(notEnded.actions[1])
        notEnded.actions[2].handler(notEnded.actions[2])
        #expect(tried && local && going)
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

    @Test func tripItemsMarkTheRunStartingNearestNowOnAFrequentRoute() throws {
        let page = RouteTripsPage(routeID: "R1", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: TripFixtures.frequentService)
        let items = CarPlayTemplates.tripItems(page, now: TripFixtures.at(14, 3), onSelect: { _ in })
        let fourteenHundred = try #require(page.trips.firstIndex { $0.id == "14:00" })
        #expect(items[fourteenHundred].detailText == "Now · A → B")
        #expect(items.indices.filter { items[$0].detailText != "A → B" } == [fourteenHundred])
    }

    @Test func listTruncatesToTheCap() {
        let items = (0..<12).map { CPListItem(text: "Route \($0)", detailText: nil) }
        let list = CarPlayTemplates.list(title: "Routes", sections: [(header: nil, items: items)], maxItems: 10, maxSections: 4)
        #expect(list.itemCount == 10)
        let last = list.sections.last?.items.last as? CPListItem
        #expect(last?.text == "Use iPhone to see more")
        #expect(last?.isEnabled == false)
        let short = CarPlayTemplates.list(title: "Routes", sections: [(header: nil, items: Array(items.prefix(3)))], maxItems: 10, maxSections: 4)
        #expect(short.itemCount == 3)
    }

    @Test func listRespectsTheSectionCap() {
        let sections = (0..<4).map { s in
            (header: "S\(s)" as String?, items: (0..<2).map { CPListItem(text: "S\(s)-\($0)", detailText: nil) })
        }
        let list = CarPlayTemplates.list(title: "Routes", sections: sections, maxItems: 100, maxSections: 2)
        #expect(list.sections.count == 2)
        #expect(list.itemCount == 8)
        #expect(list.sections[0].header == "S0")
        #expect(list.sections[0].items.count == 2)
        // The overflow joins the last section CarPlay will show, under its own
        // header, rather than being dropped.
        #expect(list.sections[1].header == "S1")
        #expect(list.sections[1].items.compactMap { ($0 as? CPListItem)?.text } == ["S1-0", "S1-1", "S2-0", "S2-1", "S3-0", "S3-1"])
        let under = CarPlayTemplates.list(title: "Routes", sections: sections, maxItems: 100, maxSections: 8)
        #expect(under.sections.count == 4)
    }

    @Test func listWithACapOfOneShowsOneRealItem() {
        let items = (0..<12).map { CPListItem(text: "Route \($0)", detailText: nil) }
        let list = CarPlayTemplates.list(title: "Routes", sections: [(header: nil, items: items)], maxItems: 1, maxSections: 4)
        #expect(list.itemCount == 1)
        let only = list.sections.first?.items.first as? CPListItem
        // The one row the car allows is a route, not a notice nobody can tap.
        #expect(only?.text == "Route 0")
        #expect(only?.isEnabled == true)
    }

    @Test func listItemHandlersInvokeOnSelect() {
        var pickedVehicle: Vehicle?
        let vehicles = CarPlayTemplates.vehicleItems([Vehicle(id: "bus-1", label: "Bus 1"), Vehicle(id: "bus-2", label: "Bus 2")]) { pickedVehicle = $0 }
        vehicles[1].handler?(vehicles[1]) {}
        #expect(pickedVehicle?.id == "bus-2")

        var pickedRoute: RouteInfo?
        let routes = [RouteInfo(id: "R1", shortName: "1", longName: "Straight", color: "", textColor: "", type: 3),
                      RouteInfo(id: "R2", shortName: "2", longName: "Loop", color: "", textColor: "", type: 3)]
        let sections = CarPlayTemplates.routeSections(routes, recentIDs: ["R2"]) { pickedRoute = $0 }
        let recent = sections[0].items[0]
        recent.handler?(recent) {}
        #expect(pickedRoute?.id == "R2")
        let all = sections[1].items[0]
        all.handler?(all) {}
        #expect(pickedRoute?.id == "R1")

        var pickedTrip: TripSummary?
        let page = RouteTripsPage(routeID: "R1", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: [
            TripSummary(id: "a", headsign: "North", directionID: 0, startsAt: TripFixtures.at(7, 0), endsAt: TripFixtures.at(7, 30), firstStop: "A", lastStop: "B"),
            TripSummary(id: "b", headsign: "North", directionID: 0, startsAt: TripFixtures.at(8, 0), endsAt: TripFixtures.at(8, 30), firstStop: "A", lastStop: "B"),
        ])
        let trips = CarPlayTemplates.tripItems(page, now: TripFixtures.at(8, 10)) { pickedTrip = $0 }
        trips[1].handler?(trips[1]) {}
        #expect(pickedTrip?.id == "b")
    }
}
