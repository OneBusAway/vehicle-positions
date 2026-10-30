import Foundation
import Testing
@testable import VehicleTracker

@Suite struct TripStatusTests {
    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!
    let active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: ""), trip: TripFixtures.t1,
                            startedAt: TripFixtures.at(7, 55), driverEmail: "d@test.com")

    /// A fix at ST1, which is scheduled at exactly 08:00, so the status
    /// follows from `secondsLate` alone. The next stop is ST2, 500 m on, at
    /// 08:05.
    func judged(secondsLate: TimeInterval) -> Adherence {
        evaluator.evaluate(TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(8, 0).addingTimeInterval(secondsLate)), previous: nil)
    }

    /// Well east of ST2: off the route, heading for ST3.
    func offRoute() -> Adherence {
        evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3200, at: TripFixtures.at(8, 5)), previous: nil)
    }

    func status(_ latest: Adherence?, reporting: TripSession.ReportingStatus = .connected,
                phase: TripSession.Phase? = nil) throws -> TripStatus {
        try #require(TripStatus(phase: phase ?? .active(active), latest: latest, reporting: reporting))
    }

    // MARK: The card

    @Test func waitingForGPSNamesTheFirstStopAndTheRun() throws {
        let s = try status(nil)
        #expect(s.answer == .waiting)
        #expect(s.card.tone == .neutral)
        #expect(s.card.symbol == "location.magnifyingglass")
        #expect(s.card.answer == "Waiting for GPS")
        #expect(s.card.label == "From")
        #expect(s.card.emphasis == "Stop ST1")
        #expect(s.card.line2.hasPrefix("8:00"))
        #expect(s.card.line2.hasSuffix(" run · 4317"))
        #expect(s.sentence.hasPrefix("Waiting for GPS. From Stop ST1, 8:00"))
        #expect(s.sentence.hasSuffix(" run, vehicle 4317"))
        #expect(s.ring == .neutral)
        #expect(s.vehicleRowStatus == nil)
        #expect(s.nextStopIndex == nil)
        #expect(s.runTime?.hasPrefix("8:00") == true)
        #expect(s.vehicleName == "4317")
    }

    @Test func onTimeNamesTheNextStop() throws {
        let s = try status(judged(secondsLate: 0))
        #expect(s.answer == .onTime)
        #expect(s.card.tone == .onTime)
        #expect(s.card.symbol == "checkmark.circle.fill")
        #expect(s.card.answer == "On time")
        #expect(s.card.label == "Next")
        #expect(s.card.emphasis == "Stop ST2")
        #expect(s.card.line2.hasPrefix("8:05"))
        #expect(s.card.line2.hasSuffix(" · 500 m"))
        #expect(s.sentence.hasPrefix("On time. Next stop Stop ST2, 8:05"))
        #expect(s.sentence.hasSuffix(", 500 metres"))
        #expect(s.ring == .onTime)
        #expect(s.vehicleRowStatus == "On time")
        #expect(s.nextStopIndex == 1)
        #expect(s.problem == nil)
        #expect(!s.isSignedOut)
    }

    @Test func lateCountsTheMinutes() throws {
        let s = try status(judged(secondsLate: 360))
        #expect(s.answer == .late)
        #expect(s.card.tone == .late)
        #expect(s.card.symbol == "hourglass.bottomhalf.filled")
        #expect(s.card.answer == "6 min late")
        #expect(s.card.label == "Next")
        #expect(s.card.emphasis == "Stop ST2")
        #expect(s.card.line2.hasPrefix("8:05"))
        #expect(s.card.line2.hasSuffix(" · 500 m"))
        #expect(s.sentence.hasPrefix("6 min late. Next stop Stop ST2, 8:05"))
        #expect(s.ring == .late)
        #expect(s.vehicleRowStatus == "6 min late")
    }

    @Test func earlyCountsTheMinutes() throws {
        let s = try status(judged(secondsLate: -120))
        #expect(s.answer == .early)
        #expect(s.card.tone == .early)
        #expect(s.card.symbol == "gauge.with.dots.needle.67percent")
        #expect(s.card.answer == "2 min early")
        #expect(s.card.label == "Next")
        #expect(s.card.emphasis == "Stop ST2")
        #expect(s.sentence.hasPrefix("2 min early. Next stop Stop ST2, 8:05"))
        #expect(s.ring == .early)
        #expect(s.vehicleRowStatus == "2 min early")
    }

    @Test func offScheduleAsksAboutTheRun() throws {
        let early = try status(judged(secondsLate: -1260))
        #expect(early.answer == .offSchedule)
        #expect(early.card.tone == .early)
        #expect(early.card.symbol == "calendar.badge.exclamationmark")
        #expect(early.card.answer == "21 min early")
        #expect(early.card.label == "Off schedule")
        #expect(early.card.emphasis == "Is this the right run?")
        #expect(early.card.line2.hasPrefix("Next Stop ST2 · 8:05"))
        #expect(early.sentence.hasPrefix("21 min early. Off schedule, is this the right run? Next stop Stop ST2, 8:05"))
        #expect(early.ring == .early)
        #expect(early.vehicleRowStatus == "21 min early", "the stop list shows the exact minutes")

        let late = try status(judged(secondsLate: 6000))
        #expect(late.answer == .offSchedule)
        #expect(late.card.tone == .late)
        #expect(late.card.answer == "100 min late")
        #expect(late.ring == .late)
    }

    @Test func offRouteSaysHowFarAndReadsNoDistanceAloud() throws {
        var off = offRoute()
        try #require(off.status == .offRoute)
        off.projection.distanceToShape = 98
        off.distanceToNextStop = 233
        let s = try status(off)
        #expect(s.answer == .offRoute)
        #expect(s.card.tone == .neutral)
        #expect(s.card.symbol == "mappin.slash")
        #expect(s.card.answer == "Off route · 98 m")
        #expect(s.card.label == "Next")
        #expect(s.card.emphasis == "Stop ST3")
        #expect(s.card.line2.hasPrefix("8:10"))
        #expect(s.card.line2.hasSuffix(" · 233 m"))
        #expect(s.sentence.hasPrefix("Off route. Next stop Stop ST3, 8:10"))
        #expect(!s.sentence.contains("98"))
        #expect(!s.sentence.contains("233"))
        #expect(!s.sentence.contains("metres"))
        #expect(s.ring == .neutral)
        #expect(s.vehicleRowStatus == "Off route")
        #expect(s.nextStopIndex == 2)
    }

    /// Inside the on-time window the card says On time, but the stop list's
    /// vehicle row still counts the minutes (P33).
    @Test func theVehicleRowCountsTheMinutesTheCardHides() throws {
        let s = try status(judged(secondsLate: 180))
        #expect(s.card.answer == "On time")
        #expect(s.vehicleRowStatus == "3 min late")
    }

    /// A loop calls at the same stop twice; the next stop is found by its
    /// sequence as well as its id, so the second call is not mistaken for
    /// the first.
    @Test func theNextStopIndexTellsTwoCallsAtOneStopApart() throws {
        var loop = TripFixtures.loop
        loop.stops[1].id = "A"
        let trip = ActiveTrip(serverTripID: 7, vehicle: active.vehicle, trip: loop, startedAt: active.startedAt, driverEmail: active.driverEmail)
        let evaluator = try #require(AdherenceEvaluator(trip: loop))
        let lateInLoop = evaluator.evaluate(TripFixtures.fix(lat: 47.6001, lon: -122.3292, at: TripFixtures.at(9, 15)), previous: nil)
        try #require(lateInLoop.nextStop.sequence == 2)
        let s = try #require(TripStatus(phase: .active(trip), latest: lateInLoop, reporting: .connected))
        #expect(s.nextStopIndex == 1)
    }

    /// The window's edges are the evaluator's: inside it the card says On
    /// time whatever the minutes, and the first second outside it counts them.
    @Test(arguments: [
        (-60.0, TripStatus.Answer.onTime, "On time"),
        (-61.0, .early, "1 min early"),
        (300.0, .onTime, "On time"),
        (301.0, .late, "5 min late"),
    ])
    func theWindowEdgesComeFromTheEvaluator(secondsLate: Double, answer: TripStatus.Answer, text: String) throws {
        let s = try status(judged(secondsLate: secondsLate))
        #expect(s.answer == answer)
        #expect(s.card.answer == text)
    }

    @Test func deviationRoundsToTheNearestMinute() {
        #expect(TripStatusText.deviation(0) == "On time")
        #expect(TripStatusText.deviation(59) == "On time")
        #expect(TripStatusText.deviation(-59) == "On time")
        #expect(TripStatusText.deviation(60) == "1 min late")
        #expect(TripStatusText.deviation(90) == "2 min late")
        #expect(TripStatusText.deviation(-90) == "2 min early")
        #expect(TripStatusText.deviation(200) == "3 min late")
        #expect(TripStatusText.deviation(3600) == "60 min late")
    }

    @Test func theVehicleIsItsLabelWhenTheFeedGivesOne() throws {
        let labelled = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: "Shuttle 4317"), trip: TripFixtures.t1,
                                  startedAt: TripFixtures.at(7, 55), driverEmail: "d@test.com")
        let s = try #require(TripStatus(phase: .active(labelled), latest: nil, reporting: .connected))
        #expect(s.vehicleName == "Shuttle 4317")
        #expect(s.card.line2.hasSuffix(" run · Shuttle 4317"))
        #expect(s.sentence.hasSuffix(" run, vehicle Shuttle 4317"))
    }

    @Test func aTripWithNoStopsHasNothingToNameWhileWaiting() throws {
        var bare = TripFixtures.t1
        bare.stops = []
        let trip = ActiveTrip(serverTripID: 7, vehicle: active.vehicle, trip: bare, startedAt: active.startedAt, driverEmail: active.driverEmail)
        let s = try #require(TripStatus(phase: .active(trip), latest: nil, reporting: .connected))
        #expect(s.card.answer == "Waiting for GPS")
        #expect(s.card.label == "")
        #expect(s.card.emphasis == "")
        #expect(s.card.line2 == "")
        #expect(s.sentence == "Waiting for GPS")
        #expect(s.runTime == nil)
    }

    @Test func thereIsNoStatusWithoutATrip() {
        #expect(TripStatus(phase: .signedOut, latest: nil, reporting: .connected) == nil)
        #expect(TripStatus(phase: .idle, latest: nil, reporting: .connected) == nil)
        #expect(TripStatus(phase: .starting, latest: nil, reporting: .connected) == nil)
        #expect(TripStatus(phase: .ending(active), latest: nil, reporting: .connected) != nil)
    }

    // MARK: Problems

    nonisolated struct ProblemCase: Sendable, CustomTestStringConvertible {
        let reporting: TripSession.ReportingStatus
        let kind: TripStatus.Problem.Kind
        let symbol: String
        let what: String
        let meaning: String
        let action: TripStatus.Problem.Action?
        let reports: TripStatus.Reports.Word
        /// Neutral while there is no position to report; the card's tone otherwise.
        let ring: StatusTone

        var testDescription: String { "\(reporting)" }
    }

    /// DESIGN-iOS §4.7.2's table, rows 6j-1 to 6j-7 and 6j-9.
    nonisolated static let problems: [ProblemCase] = [
        ProblemCase(reporting: .locationLost, kind: .locationStopped, symbol: "location.slash.fill",
                    what: "Location stopped", meaning: "Tap Resume", action: .resume, reports: .noGPS, ring: .neutral),
        ProblemCase(reporting: .needsForeground, kind: .needsForeground, symbol: "iphone",
                    what: "Open the app on iPhone", meaning: "Reports start when you do", action: nil, reports: .notReporting, ring: .onTime),
        ProblemCase(reporting: .locationDenied, kind: .locationDenied, symbol: "location.slash.fill",
                    what: "Location off", meaning: "Tap to fix", action: .openSettings, reports: .noGPS, ring: .neutral),
        ProblemCase(reporting: .preciseLocationOff, kind: .preciseLocationOff, symbol: "scope",
                    what: "Precise Location off", meaning: "Tap to fix", action: .openSettings, reports: .noGPS, ring: .neutral),
        ProblemCase(reporting: .noGPS, kind: .noGPS, symbol: "location.slash.fill",
                    what: "No GPS signal", meaning: "Position paused", action: nil, reports: .noGPS, ring: .neutral),
        ProblemCase(reporting: .refused, kind: .refused, symbol: "hand.raised.fill",
                    what: "Reports refused", meaning: "Ask your dispatcher", action: nil, reports: .notReporting, ring: .onTime),
        ProblemCase(reporting: .clockSkew, kind: .clockSkew, symbol: "clock.badge.exclamationmark",
                    what: "Phone clock is wrong", meaning: "Set automatic time", action: nil, reports: .notReporting, ring: .onTime),
        ProblemCase(reporting: .noNetwork, kind: .noNetwork, symbol: "icloud.slash.fill",
                    what: "No connection", meaning: "Riders can't see you", action: nil, reports: .notReporting, ring: .onTime),
    ]

    @Test(arguments: problems) func eachProblemHasItsLine(_ expected: ProblemCase) throws {
        let s = try status(judged(secondsLate: 0), reporting: expected.reporting)
        let problem = try #require(s.problem)
        #expect(problem.kind == expected.kind)
        #expect(problem.symbol == expected.symbol)
        #expect(problem.what == expected.what)
        #expect(problem.meaning == expected.meaning)
        #expect(problem.action == expected.action)
        #expect(s.reports.word == expected.reports)
        #expect(s.reports.symbol == expected.symbol, "the trip bar shows the problem's symbol")
        #expect(s.reports.text == (expected.reports == .noGPS ? "No GPS" : "Not reporting"))
        #expect(s.reports.isProblem)
        #expect(s.ring == expected.ring)
        #expect(!s.isSignedOut)
        #expect(s.card.answer == "On time", "the card keeps the last schedule answer")
        #expect(s.category.problem == expected.kind)
    }

    @Test func allWellIsReporting() throws {
        let s = try status(judged(secondsLate: 0))
        #expect(s.problem == nil)
        #expect(s.reports.word == .reporting)
        #expect(s.reports.symbol == "checkmark.icloud.fill")
        #expect(!s.reports.isProblem)
        #expect(s.reports.text == "Reporting")
        #expect(s.category.problem == nil)
    }

    @Test func theReportsWords() {
        #expect(TripStatus.Reports(word: .reporting, symbol: "").text == "Reporting")
        #expect(TripStatus.Reports(word: .notReporting, symbol: "").text == "Not reporting")
        #expect(TripStatus.Reports(word: .noGPS, symbol: "").text == "No GPS")
        #expect(TripStatus.Reports(word: .stopped, symbol: "").text == "Reports stopped")
    }

    @Test func signedOutReplacesTheCardAndStopsTheReports() throws {
        let s = try status(judged(secondsLate: 0), reporting: .authExpired)
        #expect(s.isSignedOut)
        #expect(s.problem == nil, "the Signed out card takes the status card's place")
        #expect(s.reports.word == .stopped)
        #expect(s.reports.symbol == "lock.fill")
        #expect(s.reports.text == "Reports stopped")
        #expect(s.ring == .neutral)
        #expect(s.card.answer == "On time", "the card keeps the last answer for when the driver signs in")
        #expect(s.category.isSignedOut)
    }

    /// A trip a relaunch found is not reporting anything until the driver
    /// taps Resume, whatever the stale status said.
    @Test func pausedIsLocationStopped() throws {
        let s = try status(nil, phase: .paused(active))
        #expect(s.problem?.kind == .locationStopped)
        #expect(s.problem?.what == "Location stopped")
        #expect(s.problem?.action == .resume)
        #expect(s.reports.word == .noGPS)
        #expect(s.ring == .neutral)

        let stale = try status(judged(secondsLate: 0), reporting: .noNetwork, phase: .paused(active))
        #expect(stale.problem?.kind == .locationStopped, "paused outranks a send problem")
    }

    /// Signing in comes before Resume: a driver who is signed out and has
    /// also lost the location stream sees Sign in first, and Location stopped
    /// once signed in again.
    @Test func signedOutOutranksEveryOtherProblem() throws {
        let paused = try status(nil, reporting: .authExpired, phase: .paused(active))
        #expect(paused.isSignedOut)
        #expect(paused.problem == nil)
        #expect(paused.reports.word == .stopped)
    }

    @Test func theCategoryIgnoresTheMinutesButNotTheKind() throws {
        let six = try status(judged(secondsLate: 360))
        let seven = try status(judged(secondsLate: 420))
        #expect(six.category == seven.category, "6 min late becoming 7 min late is not announced")
        #expect(six.card.answer != seven.card.answer)
        #expect(try status(judged(secondsLate: 0)).category != six.category, "late becoming on time is")
        #expect(try status(judged(secondsLate: 360), reporting: .noNetwork).category != six.category, "so is a problem appearing")
        #expect(try status(judged(secondsLate: 360), reporting: .authExpired).category != six.category, "and signing out")

        var near = offRoute()
        var far = near
        near.projection.distanceToShape = 98
        far.projection.distanceToShape = 300
        #expect(try status(near).category == status(far).category, "off route, the distance moves with every fix")
    }

    // MARK: Counts

    /// Read back through the String Catalog, which carries the plural forms:
    /// without its entry, English would say "1 reports were sent".
    @Test func countsReadAsOneOrMany() {
        #expect(TripStatusText.sent(1) == "1 sent")
        #expect(TripStatusText.sent(142) == "142 sent")
        #expect(TripStatusText.stopsPassed(1) == "1 stop passed")
        #expect(TripStatusText.stopsPassed(6) == "6 stops passed")
        #expect(TripStatusText.endTripMessage(route: "7", time: "6:58 AM", count: 1)
            == "Reports stop, and route 7's 6:58 AM run is marked complete. 1 report was sent.")
        #expect(TripStatusText.endTripMessage(route: "7", time: "6:58 AM", count: 142)
            == "Reports stop, and route 7's 6:58 AM run is marked complete. 142 reports were sent.")
        #expect(TripStatusText.endTripMessage(count: 1) == "Reports stop, and this run is marked complete. 1 report was sent.")
        #expect(TripStatusText.endTripMessage(count: 142) == "Reports stop, and this run is marked complete. 142 reports were sent.")
        #expect(TripStatusText.spokenDistance(1) == "1 metre")
        #expect(TripStatusText.spokenDistance(232) == "232 metres")
        #expect(TripStatusText.spokenDistance(1250) == "1.3 kilometres")
    }
}
