import Foundation
import Testing
@testable import VehicleTracker

/// The tips' and First trip done's words, as the driver reads them and as
/// VoiceOver says them (walkthrough spec §8, the iOS column).
@Suite struct TipTextTests {
    /// iOS asks "Start the 7:13 AM run?" before a trip starts, so the tip
    /// names that step, in the app's own word for it.
    @Test func theRunBoardTipNamesStartTrip() {
        #expect(TipText.runBoardTitle == "Tap your run")
        #expect(TipText.runBoardMessage == "Then tap Start trip. Riders see your vehicle from then on.")
        #expect(TipText.runBoardMessage.contains(PickerText.startTrip))
    }

    @Test func theSentCountTipSaysToKeepDriving() {
        #expect(TipText.sentCountTitle == "Reports sent")
        #expect(TipText.sentCountMessage == "This number grows while riders can see you. With no signal, keep driving.")
    }

    /// The count is one string, so VoiceOver reads it as one: "142 reports
    /// sent", "1 report sent".
    @Test func firstTripDoneReadsTheCountAsOneSentence() {
        #expect(TipText.firstTripDone == "First trip done")
        #expect(TipText.reportsSent(142) == "142 reports sent")
        #expect(TipText.reportsSent(1) == "1 report sent")
        #expect(TipText.ridersCouldSeeYou == "Riders could see your vehicle.")
        #expect(TipText.ok == "OK")
    }

    /// The sheet draws the count large, so it finds the count in whatever
    /// digits the language's region writes it in.
    @Test(arguments: ["en_US", "ar_EG", "fa_IR"])
    func firstTripDoneFindsTheCountInTheSentencesDigits(_ id: String) throws {
        let locale = Locale(identifier: id)
        let text = AttributedString(TipText.reportsSent(142, locale: locale))
        let number = try #require(FirstTripDoneSheet.countRange(142, in: text, locale: locale))
        let digits: String = 142.formatted(.number.locale(locale))
        #expect(String(text[number].characters) == digits)
    }

    @Test func noWordSaysBus() {
        let words = [TipText.runBoardTitle, TipText.runBoardMessage, TipText.sentCountTitle, TipText.sentCountMessage,
                     TipText.firstTripDone, TipText.reportsSent(2), TipText.ridersCouldSeeYou, TipText.ok]
        #expect(words.allSatisfy { !$0.localizedCaseInsensitiveContains("bus") })
    }
}
