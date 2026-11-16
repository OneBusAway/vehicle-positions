import Testing
import UIKit
@testable import VehicleTracker

/// The welcome's words, as the driver reads them and as VoiceOver says them
/// (walkthrough spec §4 and §7, the iOS column).
@Suite struct WelcomeTextTests {
    @Test func eachScreenHasItsTitleSentenceAndButton() {
        #expect(WelcomeStep.allCases.map(\.title) == ["Riders can see you coming", "Vehicle, route, run", "Keep it reporting"])
        #expect(WelcomeStep.allCases.map(\.sentence) == [
            "Start a trip and riders see your vehicle on their map. End it when you finish.",
            "Choose them, then tap Start trip. End the trip when you finish.",
            "Do these 3 things on every trip.",
        ])
        #expect(WelcomeStep.allCases.map(\.button) == ["Next", "Next", "Continue"])
        #expect(WelcomeText.skip == "Skip")
        #expect(WelcomeText.back == "Back")
    }

    /// The route line is one element.
    @Test func theRouteLineSaysWhichStepOfThree() {
        #expect(WelcomeStep.allCases.map(\.spokenStep) == ["Step 1 of 3", "Step 2 of 3", "Step 3 of 3"])
    }

    @Test func theRidersPictureIsOneSentence() {
        #expect(WelcomeText.ridersPicture == "Three riders at a stop can see your vehicle coming.")
    }

    /// iOS asks "Start the 7:13 AM run?" before a trip starts, so the run's
    /// card says to tap Start trip, where Android's says a tap starts it.
    @Test func theChoicesAreThePickersInTheirOrderEachWithItsHint() {
        #expect(Choice.allCases.map(\.title) == [PickerText.vehiclesTitle, PickerText.routesTitle, PickerText.runsTitle])
        #expect(Choice.allCases.map(\.title) == ["Choose your vehicle", "Choose a route", "Choose a run"])
        #expect(Choice.allCases.map(\.hint) == ["The number on the vehicle", "The number on the sign", "Then tap Start trip"])
        #expect(Choice.allCases.map(\.spoken) == [
            "Choose your vehicle. The number on the vehicle.",
            "Choose a route. The number on the sign.",
            "Choose a run. Then tap Start trip.",
        ])
    }

    /// The green card is read without its count, and the last row names the
    /// button it pictures without being one.
    @Test func whatFollowsTheChoicesIsReadAsTwoSentences() {
        #expect(WelcomeText.ridersSeeYou == "Riders see you")
        #expect(WelcomeText.spokenRidersSeeYou == "Riders see you.")
        #expect(WelcomeText.whenYouFinish == "When you finish")
        #expect(WelcomeText.spokenWhenYouFinish == "When you finish, End trip.")
    }

    /// iOS asks for location while the app is in use and never for Always,
    /// and stops reporting when the app is closed: its rows say so.
    @Test func theThreeNeedsEachGiveTheirReason() {
        #expect(Need.allCases.map(\.title) == ["Allow location", "Keep the phone on charge", "Leave the app open"])
        #expect(Need.allCases.map(\.reason) == [
            "Keep Precise on, so riders see the right street",
            "The screen stays on while it charges",
            "If you close it, reports stop",
        ])
        #expect(Need.allCases.map(\.spoken) == [
            "Allow location. Keep Precise on, so riders see the right street.",
            "Keep the phone on charge. The screen stays on while it charges.",
            "Leave the app open. If you close it, reports stop.",
        ])
    }

    /// Until the Live Activity brings "Reports paused", a stopped trip is the
    /// Resume screen, so the card asks that screen's question.
    @Test func theStoppedCardShowsWhatTheDriverWillSee() {
        #expect(WelcomeText.ifReportingStops == "If reporting stops")
        #expect(WelcomeText.stoppedTitle == ResumeText.title)
        #expect(WelcomeText.stoppedDetail == "Open the app and tap Resume trip.")
        #expect(WelcomeText.spokenStopped == "Resume your trip? Open the app and tap Resume trip.")
    }

    /// The buttons a screen names are the app's own words for them.
    @Test func theWordsNameTheAppsOwnButtons() {
        #expect(WelcomeText.choicesSentence.contains(PickerText.startTrip))
        #expect(WelcomeText.runHint.contains(PickerText.startTrip))
        #expect(WelcomeText.stoppedDetail.contains(ResumeText.resumeTrip))
        #expect(WelcomeText.spokenWhenYouFinish.contains(TripStatusText.endTrip))
    }

    @Test func nothingSaysBus() {
        let words = WelcomeStep.allCases.flatMap { [$0.title, $0.sentence] }
            + Choice.allCases.flatMap { [$0.title, $0.hint] } + Need.allCases.flatMap { [$0.title, $0.reason] }
            + [WelcomeText.ridersPicture, WelcomeText.ridersSeeYou, WelcomeText.whenYouFinish, WelcomeText.ifReportingStops,
               WelcomeText.stoppedTitle, WelcomeText.stoppedDetail]
        for sentence in words {
            #expect(!sentence.localizedCaseInsensitiveContains("bus"), "\(sentence)")
        }
    }

    @Test(arguments: Choice.allCases.map(\.symbol) + Need.allCases.map(\.symbol) + [
        "chevron.left", "figure.stand", "figure.wave", "person.fill", "checkmark", "checkmark.circle.fill",
        "checkmark.icloud.fill", "stop.circle", "pause.circle.fill",
    ])
    func everySymbolIsInTheSystemSet(symbol: String) {
        #expect(UIImage(systemName: symbol) != nil)
    }
}
