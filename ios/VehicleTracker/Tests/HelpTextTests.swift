import Testing
@testable import VehicleTracker

/// Help's own words: each row's plain line under the words Tracking shows,
/// and the page's headings (walkthrough spec §9, the iOS column).
@Suite struct HelpTextTests {
    @Test func eachStatusRowSaysWhatTheCardMeans() {
        #expect(StatusHelp.allCases.map(\.meaning) == [
            "You are on the timetable.",
            "Behind the timetable.",
            "Ahead of the timetable. Riders may miss you.",
            "Far from the timetable. Check you chose the right run.",
            "Away from the route on the map.",
            "The iPhone is finding its position.",
        ])
    }

    /// iOS drops a report it cannot send, so No connection does not say
    /// reports are kept, as Android's does.
    @Test func eachProblemRowSaysWhatToDo() {
        #expect(ProblemHelp.allCases.map(\.meaning) == [
            "Keep driving. Reports start again with the signal.",
            "Riders see your last position until it comes back.",
            "Tap the line. It opens Settings to turn it on.",
            "Tap the line. It opens Settings to turn it on.",
            "Tap Resume on the same line.",
            "Turn on automatic time in Settings.",
            "The server is not taking this vehicle's reports. Ask your dispatcher.",
            "A problem on the server. Tell your dispatcher.",
            "Reports have stopped. Sign in to go on. Your trip is still open.",
            "With CarPlay. Reports start when you open the app.",
        ])
        #expect(!ProblemHelp.noConnection.meaning.localizedCaseInsensitiveContains("saved"))
    }

    /// Location stopped's line names the button that is on it.
    @Test func locationStoppedNamesTheResumeButton() {
        #expect(ProblemHelp.locationStopped.meaning.contains(TripStatusText.resume))
    }

    @Test func thePagesOwnWords() {
        #expect(HelpText.title == "Help")
        #expect(HelpText.welcomeAgain == "See the welcome again")
        #expect(HelpText.welcomeAgainDetail == "3 short screens")
        #expect(HelpText.statusSection == "On the status card")
        #expect(HelpText.problemSection == "Under the card")
        #expect(HelpText.aboutSection == "About")
        #expect(HelpText.version == "Version")
        #expect(HelpText.shareDiagnostics == "Share diagnostics")
        #expect(HelpText.shareDiagnosticsDetail == "Sends the app version, the iPhone model and recent errors. No positions, no sign-in details, no email.")
    }

    @Test func noWordSaysBus() {
        let words = [HelpText.title, HelpText.welcomeAgain, HelpText.welcomeAgainDetail, HelpText.statusSection,
                     HelpText.problemSection, HelpText.aboutSection, HelpText.version, HelpText.shareDiagnostics,
                     HelpText.shareDiagnosticsDetail]
            + StatusHelp.allCases.flatMap { [$0.title, $0.meaning] }
            + ProblemHelp.allCases.flatMap { [$0.title, $0.meaning] }
        #expect(words.allSatisfy { !$0.localizedCaseInsensitiveContains("bus") })
    }
}
