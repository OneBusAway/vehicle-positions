import Foundation

/// Every word the two tips and First trip done show or say (walkthrough spec
/// §8, the iOS column). The button a tip names is the app's own word for it.
nonisolated enum TipText {
    // MARK: Choose a run

    static var runBoardTitle: String {
        String(localized: "Tap your run", comment: "Tip on 'Choose a run', pointing at the run under way: its title.")
    }

    /// iOS asks "Start the 7:13 AM run?" before a trip starts, so the tip
    /// names that step.
    static var runBoardMessage: String {
        String(localized: "Then tap \(PickerText.startTrip). Riders see your vehicle from then on.",
               comment: "Tip on 'Choose a run', under its title. The name of the start confirmation's button that starts the trip, filled in from that button, e.g. 'Then tap Start trip. Riders see your vehicle from then on.'")
    }

    // MARK: Tracking

    static var sentCountTitle: String {
        String(localized: "Reports sent", comment: "Tip on the trip bar's sent count, on the driver's first trip: its title.")
    }

    static var sentCountMessage: String {
        String(localized: "This number grows while riders can see you. With no signal, keep driving.",
               comment: "Tip on the trip bar's sent count, under its title.")
    }

    // MARK: First trip done

    static var firstTripDone: String {
        String(localized: "First trip done", comment: "Title of the sheet after the driver's first trip ends.")
    }

    /// "142 reports sent". The sheet draws the number large.
    static func reportsSent(_ count: Int, locale: Locale = .current) -> String {
        String(localized: "\(count) reports sent", locale: locale, comment: "First trip done: how many reports the trip sent, e.g. '142 reports sent'. The number is drawn large wherever it stands in the sentence.")
    }

    static var ridersCouldSeeYou: String {
        String(localized: "Riders could see your vehicle.", comment: "First trip done: under the sent count.")
    }

    static var ok: String {
        String(localized: "OK", comment: "First trip done: the button that closes the sheet.")
    }
}
