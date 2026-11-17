import Foundation
import Testing
@testable import VehicleTracker

/// Every answer the status card gives and every line under it has its own row
/// on Help, with the words, symbol and colour Tracking shows: each sample is
/// made by `TripStatus` from a trip, as Tracking makes it (walkthrough spec §9).
@Suite struct HelpRowsTests {
    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!
    let active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: ""), trip: TripFixtures.t1,
                            startedAt: TripFixtures.at(7, 55), driverEmail: "d@test.com")

    /// A fix at ST1, scheduled at 08:00, so the answer follows from
    /// `secondsLate` alone.
    func judged(secondsLate: TimeInterval) -> Adherence {
        evaluator.evaluate(TripFixtures.fix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(8, 0).addingTimeInterval(secondsLate)), previous: nil)
    }

    func status(_ latest: Adherence?, reporting: TripSession.ReportingStatus = .connected,
                phase: TripSession.Phase? = nil) throws -> TripStatus {
        try #require(TripStatus(phase: phase ?? .active(active), latest: latest, reporting: reporting))
    }

    /// One of each answer the card gives, at the minutes Help shows.
    func everyAnswer() throws -> [TripStatus] {
        let offRoute = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3200, at: TripFixtures.at(8, 5)), previous: nil)
        return try [
            status(judged(secondsLate: 0)),
            status(judged(secondsLate: 360)),
            status(judged(secondsLate: -180)),
            status(judged(secondsLate: -1260)),
            status(offRoute),
            status(nil),
        ]
    }

    /// One of each reporting status a trip can have, and a paused trip.
    /// `kinds` stops compiling when `ReportingStatus` gets a case, until it
    /// is added here.
    func everyReportingStatus() throws -> [TripStatus] {
        let all: [TripSession.ReportingStatus] = [.connected, .noNetwork, .noGPS, .authExpired, .clockSkew, .refused,
                                                  .serverError, .needsForeground, .locationLost, .locationDenied,
                                                  .preciseLocationOff]
        func kinds(_ reporting: TripSession.ReportingStatus) {
            switch reporting {
            case .connected, .noNetwork, .noGPS, .authExpired, .clockSkew, .refused, .serverError, .needsForeground,
                 .locationLost, .locationDenied, .preciseLocationOff: break
            }
        }
        all.forEach(kinds)
        return try all.map { try status(judged(secondsLate: 0), reporting: $0) } + [status(nil, phase: .paused(active))]
    }

    // MARK: On the status card

    @Test func theSamplesAreOneOfEachAnswer() throws {
        let answers = try everyAnswer().map(\.answer)
        #expect(answers == [.onTime, .late, .early, .offSchedule, .offRoute, .waiting])
        // A new answer fails here until it has a sample above, and so a row.
        for answer in answers {
            switch answer {
            case .onTime, .late, .early, .offSchedule, .offRoute, .waiting: break
            }
        }
    }

    @Test func everyAnswerHasARowOfItsOwnAndEveryRowIsAnAnswer() throws {
        let rows = try everyAnswer().map { StatusHelp($0.answer) }
        #expect(rows == StatusHelp.allCases)
    }

    /// The card's own symbol and words. "Off schedule" is the card's line
    /// over the minutes, and "Off route" is the card's answer without its
    /// distance, as VoiceOver and the stop list say it.
    @Test func eachRowShowsTheCardsSymbolAndWords() throws {
        for card in try everyAnswer() {
            let row = StatusHelp(card.answer)
            #expect(row.symbol == card.card.symbol, "\(row)")
            switch row {
            case .offSchedule: #expect(row.title == card.card.label)
            case .offRoute: #expect(row.title == card.vehicleRowStatus)
            case .onTime, .late, .early, .waitingForGPS: #expect(row.title == card.card.answer, "\(row)")
            }
        }
        #expect(StatusHelp.late.title == "6 min late")
        #expect(StatusHelp.early.title == "3 min early")
    }

    /// Off schedule is red when the run is early, as the sample is, and blue
    /// when late; Help shows the red, as Android's does.
    @Test func eachRowIsTheCardsColour() throws {
        for card in try everyAnswer() {
            #expect(StatusHelp(card.answer).tone == card.card.tone, "\(card.answer)")
        }
        #expect(try status(judged(secondsLate: 6000)).card.tone == .late)
        #expect(StatusHelp.offSchedule.tone == .early)
    }

    // MARK: Under the card

    @Test func everyProblemHasARowOfItsOwnAndEveryRowIsAProblem() throws {
        let statuses = try everyReportingStatus()
        let rows = statuses.compactMap(ProblemHelp.init)
        #expect(statuses.count - rows.count == 1, "only Connected has nothing under the card")
        #expect(Set(rows) == Set(ProblemHelp.allCases))
        #expect(rows.count == ProblemHelp.allCases.count + 1, "Location lost and a paused trip share Location stopped")
    }

    /// The line's own symbol and words, or the Signed out card's.
    @Test func eachRowShowsTheLinesSymbolAndWords() throws {
        for status in try everyReportingStatus() {
            guard let row = ProblemHelp(status) else { continue }
            if let problem = status.problem {
                #expect(row.title == problem.what, "\(row)")
                #expect(row.symbol == problem.symbol, "\(row)")
            } else {
                #expect(status.isSignedOut)
                #expect(row == .signedOut)
                #expect(row.title == TripStatusText.signedOut)
                #expect(row.symbol == status.reports.symbol, "the lock the trip bar and the Signed out card show")
            }
        }
    }

    /// Signed out outranks every line, as on Tracking.
    @Test func signedOutIsTheRowWhateverElseIsWrong() throws {
        let paused = try status(nil, reporting: .authExpired, phase: .paused(active))
        #expect(ProblemHelp(paused) == .signedOut)
        #expect(ProblemHelp(try status(judged(secondsLate: 0))) == nil)
    }

    // MARK: The page

    /// Spec §9's order: the card's answers, then the lines under it.
    @Test func thePageRunsInTheSpecsOrder() {
        #expect(HelpPage.statusEntries.map(\.title) == [
            "On time", "6 min late", "3 min early", "Off schedule", "Off route", "Waiting for GPS",
        ])
        #expect(HelpPage.problemEntries.map(\.title) == [
            "No connection", "No GPS signal", "Location off", "Precise Location off", "Location stopped",
            "Phone clock is wrong", "Reports refused", "Server error", "Signed out", "Open the app on iPhone",
        ])
    }

    @Test func statusRowsSitOnTheCardsColourAndProblemRowsOnAttention() {
        #expect(HelpPage.statusEntries.map(\.disc) == StatusHelp.allCases.map(\.tone.fill))
        #expect(HelpPage.statusEntries.allSatisfy { $0.ink == .onStatusFill })
        #expect(HelpPage.problemEntries.allSatisfy { $0.disc == .attentionContainer && $0.ink == .amberOnSurface })
        let ids = (HelpPage.statusEntries + HelpPage.problemEntries).map(\.id)
        #expect(Set(ids).count == ids.count, "each row has its own id")
    }
}
