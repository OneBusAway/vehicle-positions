import CoreGraphics
import Testing
@testable import VehicleTracker

/// What the header of Settings says for each thing iOS can say about
/// location, and where the vehicle stands on its line.
@Suite struct ReadinessTests {
    nonisolated struct Case: Sendable, CustomTestStringConvertible {
        let access: LocationAccessState
        let title: String
        let sentence: String
        let stops: [Readiness.Stop]
        let vehicleAt: Int
        let fix: Readiness.Fix?

        var testDescription: String { "\(access)" }
    }

    nonisolated static let cases = [
        Case(access: .allowed, title: "Ready to report", sentence: "Start a trip and riders see your vehicle.",
             stops: [.allowed, .allowed, .allowed], vehicleAt: 4, fix: nil),
        Case(access: .notDetermined, title: "Not ready to report", sentence: "The app asks for location when you start a trip.",
             stops: [.missing, .beyond, .allowed], vehicleAt: 0, fix: .ask),
        Case(access: .denied, title: "Not ready to report", sentence: "Location is not allowed for this app.",
             stops: [.missing, .beyond, .allowed], vehicleAt: 0, fix: .openSettings),
        Case(access: .restricted, title: "Not ready to report", sentence: "This iPhone does not let apps use location.",
             stops: [.missing, .beyond, .beyond], vehicleAt: 0, fix: nil),
        Case(access: .approximateOnly, title: "Not ready to report", sentence: "Precise location is off for this app.",
             stops: [.allowed, .missing, .allowed], vehicleAt: 1, fix: .openSettings),
        Case(access: .servicesOff, title: "Not ready to report", sentence: "Location is off for the whole phone.",
             stops: [.beyond, .beyond, .missing], vehicleAt: 0, fix: .readAgain),
    ]

    @Test(arguments: cases) func eachAnswerHasItsWordsAndItsLine(_ expected: Case) {
        let readiness = Readiness(access: expected.access, waiting: 0)
        let words = ReadinessWords(readiness)
        #expect(words.title == expected.title)
        #expect(words.sentence == expected.sentence)
        #expect(words.symbol == nil)
        #expect(readiness.stops == expected.stops)
        #expect(readiness.vehicleAt == expected.vehicleAt)
        #expect(readiness.fix == expected.fix)
    }

    /// A trip can start wherever Before you start would not stop it, and
    /// nowhere else.
    @Test(arguments: cases) func itIsReadyWhenATripNeedsNoChecklist(_ expected: Case) {
        #expect(Readiness(access: expected.access, waiting: 0).canReport == !LocationChecklist.needed(expected.access))
    }

    /// A stop is drawn allowed only where Before you start would say so, or
    /// for Location Services, which iOS has to have on to answer for the app.
    @Test(arguments: cases) func noStopIsAllowedThatTheChecklistDoesNotKnowOf(_ expected: Case) {
        let checklist = LocationChecklist(expected.access)
        for (row, stop) in zip(LocationChecklist.Row.allCases, Readiness(access: expected.access, waiting: 0).stops)
        where stop == .allowed && row != .locationServices {
            #expect(checklist.state(of: row) == .allowed, "\(row)")
        }
    }

    /// A row has the fix only where it is what stops a trip.
    @Test(arguments: cases) func theFixIsTheMissingRowsAlone(_ expected: Case) {
        let readiness = Readiness(access: expected.access, waiting: 0)
        for (row, stop) in zip(LocationChecklist.Row.allCases, expected.stops) {
            #expect(readiness.fix(for: row) == (stop == .missing ? expected.fix : nil), "\(row)")
        }
    }

    /// The header says when the app asks rather than asking.
    @Test(arguments: cases) func theHeaderOffersTheFixUnlessItIsToAsk(_ expected: Case) {
        #expect(Readiness(access: expected.access, waiting: 0).headerFix == (expected.fix == .ask ? nil : expected.fix))
    }

    /// Where Before you start can only be put right in one way, so can the
    /// header and Permission check, under the same words: Try again reads
    /// Location Services again, as the app's page in Settings can't turn them
    /// on. Precise location is left out on purpose. Before you start's Try
    /// again asks iOS for precise location for the trip about to start, a
    /// grant that lasts while that trip's location indicator is up. Settings
    /// starts no trip, so a grant from there would be gone by the next one,
    /// and the header and Permission check offer the app's page in Settings,
    /// where it stays on.
    @Test func whatPutsItRightIsBeforeYouStarts() throws {
        for (access, row, words) in [(LocationAccessState.denied, LocationChecklist.Row.location, "Open Settings"),
                                     (.servicesOff, .locationServices, "Try again")] {
            let action = try #require(LocationChecklist(access).state(of: row).action)
            let readiness = Readiness(access: access, waiting: 0)
            let fix = try #require(readiness.fix(for: row))
            #expect(readiness.headerFix == fix)
            switch (action, fix) {
            case (.openSettings, .openSettings), (.readAgain, .readAgain): break
            default: Issue.record("\(action) on Before you start, \(fix) in Settings")
            }
            #expect(PermissionCheckText.name(of: fix) == words)
        }
    }

    @Test func reportsWaitingAreSaidOnceTheRestIsReady() {
        let waiting = ReadinessWords(Readiness(access: .allowed, waiting: 12))
        #expect(waiting.title == "Ready to report")
        #expect(waiting.sentence == "12 reports to send when online")
        #expect(waiting.symbol == StatusHelp.reportsWaiting.symbol)
        #expect(ReadinessWords(Readiness(access: .denied, waiting: 12)).sentence == "Location is not allowed for this app.")
    }

    @Test func noWordSaysBus() {
        let words = Self.cases.flatMap { [$0.title, $0.sentence] }
        #expect(words.allSatisfy { !$0.localizedCaseInsensitiveContains("bus") })
    }

    // MARK: Along the line

    @Test func theStopsAreEvenlySpacedBetweenHomeAndTheRider() {
        let geometry = LineGeometry(width: 402, stops: 3)
        let places = (0...4).map { geometry.x(at: Double($0)) }
        #expect(places.first == 36)
        #expect(places.last == 326)
        let gaps = zip(places.dropFirst(), places).map { $0 - $1 }
        #expect(gaps.allSatisfy { abs($0 - gaps[0]) < 0.001 })
        #expect(geometry.rider == 368)
    }

    @Test func aNarrowLineStartsCloserToItsEdge() {
        #expect(LineGeometry(width: 349, stops: 3).x(at: 0) == 30)
        #expect(LineGeometry(width: 350, stops: 3).x(at: 0) == 36)
        #expect(LineGeometry(width: 349, stops: 3).isNarrow)
        #expect(!LineGeometry(width: 350, stops: 3).isNarrow)
    }

    @Test func symbolsNeedRoomOverTheirStops() {
        #expect(LineGeometry(width: 320, stops: 3).hasRoomForSymbols)
        // 32 pt from stop to stop, then a little less.
        #expect(LineGeometry(width: 266, stops: 4).hasRoomForSymbols)
        #expect(!LineGeometry(width: 265, stops: 4).hasRoomForSymbols)
    }
}

/// How the header folds as the page scrolls, and where it settles.
@Suite struct HeaderFoldTests {
    let fold = HeaderFold(distance: 100)

    @Test func itFoldsWithThePageThenStays() {
        #expect(fold.fraction(0) == 0)
        #expect(fold.fraction(25) == 0.25)
        #expect(fold.fraction(100) == 1)
        #expect(fold.fraction(400) == 1)
        #expect(fold.pastFold(100) == 0)
        #expect(fold.pastFold(130) == 30)
    }

    /// Pulled down past its top, the page takes the header with it.
    @Test func pulledDownItDoesNotFold() {
        #expect(fold.fraction(-40) == 0)
        #expect(fold.pastFold(-40) == 0)
    }

    @Test func beforeItIsMeasuredItDoesNotFold() {
        #expect(HeaderFold().fraction(40) == 0)
    }

    @Test func letGoInTheMiddleItSettlesToTheNearerEnd() {
        #expect(fold.settled(30, furthest: 500) == 0)
        #expect(fold.settled(49, furthest: 500) == 0)
        #expect(fold.settled(50, furthest: 500) == 100)
        #expect(fold.settled(99, furthest: 500) == 100)
    }

    @Test func atEitherEndOrPastItStaysWhereItIs() {
        #expect(fold.settled(-20, furthest: 500) == -20)
        #expect(fold.settled(0, furthest: 500) == 0)
        #expect(fold.settled(100, furthest: 500) == 100)
        #expect(fold.settled(260, furthest: 500) == 260)
    }

    /// Settling would put it back each time, so its last rows could never be
    /// read.
    @Test func aPageTooShortToFoldItAllIsLeftAlone() {
        #expect(fold.settled(60, furthest: 80) == 60)
        #expect(fold.settled(60, furthest: 100) == 100)
    }

    @Test func theWordsAreGoneBeforeItHasFolded() {
        #expect(fold.wordsOpacity(30) == 1)
        #expect(fold.wordsOpacity(80) == 0)
        #expect(fold.symbolsOpacity(30) == 1)
        #expect(fold.symbolsOpacity(70) == 0)
        #expect(!fold.showsBarLine(79))
        #expect(fold.showsBarLine(80))
        #expect(!HeaderFold().showsBarLine(0))
        #expect(fold.vehicleScale(0) == 1)
        #expect(abs(fold.vehicleScale(100) - 0.65) < 0.001)
    }
}

@Suite struct SettingsTextTests {
    @Test func thePagesOwnWords() {
        #expect(SettingsText.title == "Settings")
        #expect(SettingsText.appSection == "App")
        #expect(SettingsText.language == "Language")
        #expect(SettingsText.theme == "Theme")
        #expect(ThemeChoice.allCases.map(SettingsText.name) == ["System", "Light", "Dark"])
        #expect(SettingsText.aboutSection == "About")
        #expect(SettingsText.version == "Version")
        #expect(SettingsText.shareDiagnostics == "Share diagnostics")
        #expect(SettingsText.shareDiagnosticsDetail == "Sends the app version, the iPhone model, the server, what the app is allowed to do and recent errors. No positions, no sign-in details, no email.")
    }
}
