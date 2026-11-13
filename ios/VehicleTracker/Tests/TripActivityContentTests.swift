import Foundation
import Testing
@testable import VehicleTracker

@Suite struct TripActivityContentTests {
    typealias Content = TripActivityAttributes.ContentState

    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!
    let active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: ""), trip: TripFixtures.t1,
                            startedAt: TripFixtures.at(7, 55), driverEmail: "d@test.com")
    let eightOFive = Formatters.clock(TripFixtures.at(8, 5), timezone: "America/Los_Angeles")

    /// A fix at ST1, scheduled at exactly 08:00, so the status follows from
    /// `secondsLate` alone. The next stop is ST2, at 08:05.
    func judged(secondsLate: TimeInterval, north metres: Double = 0) -> Adherence {
        let fix = TripFixtures.fix(lat: 47.6000 + metres / 111_000, lon: -122.3300,
                                   at: TripFixtures.at(8, 0).addingTimeInterval(secondsLate))
        return evaluator.evaluate(fix, previous: nil)
    }

    /// Well east of ST2: off the route, heading for ST3.
    func offRoute() -> Adherence {
        evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3200, at: TripFixtures.at(8, 5)), previous: nil)
    }

    func content(_ latest: Adherence?, reporting: TripSession.ReportingStatus = .connected,
                 phase: TripSession.Phase? = nil, trip: ActiveTrip? = nil) throws -> Content {
        let status = try #require(TripStatus(phase: phase ?? .active(trip ?? active), latest: latest, reporting: reporting))
        return Content(status: status, latest: latest)
    }

    static func fields(_ content: Content) -> [String] {
        [content.symbol, content.lead, content.minutes, content.next, content.reportsSymbol, content.reports, content.spoken]
            .compactMap { $0 }
    }

    // MARK: The answer

    @Test func onTimeShowsTheAnswerTheNextStopAndReporting() throws {
        let c = try content(judged(secondsLate: 0))
        #expect(c.tone == .onTime)
        #expect(!c.attention)
        #expect(c.symbol == "checkmark.circle.fill")
        #expect(c.lead == "On time")
        #expect(c.minutes == nil)
        #expect(c.next == "Next Stop ST2 · \(eightOFive)")
        #expect(c.reportsSymbol == "checkmark.icloud.fill")
        #expect(c.reports == "Reporting")
        #expect(c.spoken == "Route 1 to North. On time. Next stop Stop ST2, \(eightOFive)")
    }

    @Test func lateAndEarlyCarryTheMinutesForTheIsland() throws {
        let late = try content(judged(secondsLate: 360))
        #expect(late.tone == .late)
        #expect(late.lead == "6 min late")
        #expect(late.minutes == "6 min")
        #expect(late.next == "Next Stop ST2 · \(eightOFive)")

        let early = try content(judged(secondsLate: -360))
        #expect(early.tone == .early)
        #expect(early.lead == "6 min early")
        #expect(early.minutes == "6 min")
    }

    /// The card's lines ask "Is this the right run?"; the Live Activity's
    /// next-stop line is built from the fix, with "Off schedule" in front.
    @Test func offScheduleSaysOffScheduleOnTheNextStopLine() throws {
        let c = try content(judged(secondsLate: -1260))
        #expect(c.lead == "21 min early")
        #expect(c.next == "Off schedule · Next Stop ST2 · \(eightOFive)")
        #expect(c.minutes == nil, "the Island's minutes are for late and early only")
        #expect(c.spoken == "Route 1 to North. 21 min early. Off schedule, is this the right run? Next stop Stop ST2, \(eightOFive)")
    }

    @Test func offRouteLeavesTheDistanceOut() throws {
        let c = try content(offRoute())
        #expect(c.lead == "Off route")
        #expect(c.symbol == "mappin.slash")
        for field in Self.fields(c) {
            #expect(field.range(of: #"\d ?(m|km|metres|kilometres)\b"#, options: .regularExpression) == nil, "\(field)")
        }
    }

    @Test func waitingHasNoNextStopLine() throws {
        let c = try content(nil)
        #expect(c.tone == .neutral)
        #expect(c.lead == "Waiting for GPS")
        #expect(c.next == nil)
        #expect(c.minutes == nil)
        #expect(c.reportsSymbol == "checkmark.icloud.fill")
        #expect(c.reports == "Reporting")
        let eight = Formatters.clock(TripFixtures.at(8, 0), timezone: "America/Los_Angeles")
        #expect(c.spoken == "Route 1 to North. Waiting for GPS. From Stop ST1, \(eight) run, vehicle 4317")
    }

    // MARK: Problems and signed out

    @Test(arguments: [
        (TripSession.ReportingStatus.noNetwork, "No connection", "Riders can't see you"),
        (.noGPS, "No GPS signal", "Position paused"),
        (.clockSkew, "Phone clock is wrong", "Set automatic time"),
        (.refused, "Reports refused", "Ask your dispatcher"),
        (.serverError, "Server error", "Riders can't see you"),
        (.needsForeground, "Open the app on iPhone", "Reports start when you do"),
        (.locationLost, "Location stopped", "Open the app to keep reporting."),
        (.locationDenied, "Location off", "Open the app to keep reporting."),
        (.preciseLocationOff, "Precise Location off", "Open the app to keep reporting."),
    ])
    func everyProblemKindHasItsTwoLines(reporting: TripSession.ReportingStatus, lead: String, reports: String) throws {
        let latest = judged(secondsLate: 360)
        let c = try content(latest, reporting: reporting)
        let status = try #require(TripStatus(phase: .active(active), latest: latest, reporting: reporting))
        let problem = try #require(status.problem)
        #expect(c.attention)
        #expect(c.symbol == problem.symbol)
        #expect(c.lead == lead)
        #expect(c.reports == reports)
        #expect(c.reportsSymbol == nil)
        #expect(c.minutes == nil, "the problem's symbol stands alone in the Island")
        #expect(c.next == "Next Stop ST2 · \(eightOFive)")
        #expect(c.spoken == "Route 1 to North. \(lead), \(reports)")
    }

    /// The car can't open Settings, and a Live Activity can't take a tap
    /// (#236): no word on it asks for one.
    @Test(arguments: [TripSession.ReportingStatus.connected, .noNetwork, .noGPS, .authExpired, .clockSkew, .refused,
                      .serverError, .needsForeground, .locationLost, .locationDenied, .preciseLocationOff])
    func noLiveActivityTextSaysTap(reporting: TripSession.ReportingStatus) throws {
        let fixes: [Adherence?] = [nil, judged(secondsLate: 0), judged(secondsLate: 360), judged(secondsLate: -360),
                                   judged(secondsLate: -1260), offRoute()]
        for latest in fixes {
            for phase: TripSession.Phase in [.active(active), .paused(active), .ending(active)] {
                let c = try content(latest, reporting: reporting, phase: phase)
                for field in Self.fields(c) {
                    #expect(!field.localizedCaseInsensitiveContains("tap"), "\(field)")
                }
            }
        }
    }

    @Test func signedOutHidesTheNextStopAndAsksToSignIn() throws {
        let c = try content(judged(secondsLate: 360), reporting: .authExpired)
        #expect(c.attention)
        #expect(c.symbol == "lock.fill")
        #expect(c.lead == "Signed out")
        #expect(c.next == nil)
        #expect(c.minutes == nil)
        #expect(c.reportsSymbol == nil)
        #expect(c.reports == "Open the app to sign in.")
        #expect(c.spoken == "Route 1 to North. Signed out. Reports have stopped. Your trip is still open.")
    }

    @Test func endingKeepsTheLeadAndSaysEnding() throws {
        let onTime = try content(judged(secondsLate: 0), phase: .ending(active))
        #expect(onTime.lead == "On time")
        #expect(!onTime.attention)
        #expect(onTime.reportsSymbol == "hourglass")
        #expect(onTime.reports == "Ending…")
        #expect(onTime.spoken == "Route 1 to North. On time. Next stop Stop ST2, \(eightOFive) · Ending…")

        let offline = try content(judged(secondsLate: 0), reporting: .noNetwork, phase: .ending(active))
        #expect(offline.lead == "No connection")
        #expect(offline.attention)
        #expect(offline.reportsSymbol == "hourglass")
        #expect(offline.reports == "Ending…")
        #expect(offline.spoken == "Route 1 to North. No connection · Ending…", "not what to do about the problem, which no longer applies")
    }

    // MARK: When it changes

    /// Ten metres on, in the same minute and before the same stop: nothing
    /// on the activity changes, so nothing is sent.
    @Test func twoFixesWithTheSameWordsGiveEqualContent() throws {
        #expect(try content(judged(secondsLate: 0)) == content(judged(secondsLate: 10, north: 10)))
        #expect(try content(offRoute()) == content(evaluator.evaluate(
            TripFixtures.fix(lat: 47.6046, lon: -122.3190, at: TripFixtures.at(8, 5, 10)), previous: nil)))
    }

    @Test func aNewMinuteOrANextStopChangesTheContent() throws {
        #expect(try content(judged(secondsLate: 360)) != content(judged(secondsLate: 420)))
        // Past ST2, ST3 is next.
        let pastST2 = evaluator.evaluate(TripFixtures.fix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 30)), previous: nil)
        #expect(try content(judged(secondsLate: 0)) != content(pastST2))
        #expect(try content(pastST2).next?.hasPrefix("Next Stop ST3 · ") == true)
    }

    @Test func theSentenceNamesTheRouteAndHasNoDistance() throws {
        var trip = active
        trip.trip.route.shortName = ""
        let unnamed = try content(judged(secondsLate: 0), trip: trip)
        #expect(unnamed.spoken == "North. On time. Next stop Stop ST2, \(eightOFive)")
        let named = try content(judged(secondsLate: 0))
        #expect(named.spoken.hasPrefix("Route 1 to North. "))
        #expect(!named.spoken.contains("metres"))
    }

    // MARK: Size

    /// ActivityKit refuses attributes and content over 4 KB together.
    @Test func attributesAndTheLongestContentFitInFourKilobytes() throws {
        let forty = String(repeating: "W", count: 40)
        var trip = active
        trip.trip.headsign = forty
        trip.trip.route.shortName = String(repeating: "8", count: 8)
        trip.trip.stops = trip.trip.stops.map { stop in
            var stop = stop
            stop.name = forty
            return stop
        }
        let latest = AdherenceEvaluator(trip: trip.trip)!.evaluate(
            TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(8, 0).addingTimeInterval(-1260)), previous: nil)
        let longest = try [TripSession.ReportingStatus.connected, .preciseLocationOff, .needsForeground]
            .map { try content(latest, reporting: $0, trip: trip) }
        for state in longest {
            let size = try JSONEncoder().encode(TripActivityAttributes(trip)).count + JSONEncoder().encode(state).count
            #expect(size <= 4096, "\(size) bytes")
        }
    }
}
