import Foundation

/// Every word the welcome shows or says. Where a screen names something the
/// app has, the word is the app's own: the picker titles, Start trip, End
/// trip, Resume trip and the sent count come from the screens that show
/// them, so the welcome cannot drift from what the driver meets next.
nonisolated enum WelcomeText {
    static var skip: String {
        String(localized: "Skip", comment: "Welcome: the button on every screen that closes the welcome.")
    }

    static var back: String {
        String(localized: "Back", comment: "VoiceOver: the welcome's button to the screen before.")
    }

    static var next: String {
        String(localized: "Next", comment: "Welcome: the button to the next of its three screens.")
    }

    static var `continue`: String {
        String(localized: "Continue", comment: "Welcome: the button on its last screen, which goes on to choosing a vehicle.")
    }

    static func step(_ number: Int, of count: Int) -> String {
        String(localized: "Step \(number) of \(count)", comment: "VoiceOver: the welcome's progress line, e.g. 'Step 1 of 3'.")
    }

    // MARK: Riders can see you coming

    static var ridersTitle: String {
        String(localized: "Riders can see you coming", comment: "Welcome, screen 1: title.")
    }

    static var ridersSentence: String {
        String(localized: "Start a trip and riders see your vehicle on their map. End it when you finish.", comment: "Welcome, screen 1: under the title.")
    }

    static var ridersPicture: String {
        String(localized: "Three riders at a stop can see your vehicle coming.", comment: "VoiceOver: the picture on the welcome's first screen, a vehicle on its route, riders waving at the next stop and a rider's phone showing the vehicle.")
    }

    // MARK: Vehicle, route, run

    static var choicesTitle: String {
        String(localized: "Vehicle, route, run", comment: "Welcome, screen 2: title. The three things a driver chooses before a trip.")
    }

    static var choicesSentence: String {
        String(localized: "Choose them, then tap \(PickerText.startTrip). End the trip when you finish.", comment: "Welcome, screen 2: under the title. The name of the button that starts a trip.")
    }

    static var vehicleHint: String {
        String(localized: "The number on the vehicle", comment: "Welcome, screen 2: under 'Choose your vehicle'.")
    }

    static var routeHint: String {
        String(localized: "The number on the sign", comment: "Welcome, screen 2: under 'Choose a route'.")
    }

    static var runHint: String {
        String(localized: "Then tap \(PickerText.startTrip)", comment: "Welcome, screen 2: under 'Choose a run'. The name of the button that starts a trip.")
    }

    static var ridersSeeYou: String {
        String(localized: "Riders see you", comment: "Welcome, screen 2: the green card that follows the three choices.")
    }

    static var whenYouFinish: String {
        String(localized: "When you finish", comment: "Welcome, screen 2: beside a picture of the End trip button.")
    }

    // MARK: Keep it reporting

    static var reportingTitle: String {
        String(localized: "Keep it reporting", comment: "Welcome, screen 3: title.")
    }

    static var reportingSentence: String {
        String(localized: "Do these 3 things on every trip.", comment: "Welcome, screen 3: under the title.")
    }

    static var allowLocation: String {
        String(localized: "Allow location", comment: "Welcome, screen 3: the first thing a trip needs.")
    }

    static var allowLocationReason: String {
        String(localized: "Keep Precise on, so riders see the right street", comment: "Welcome, screen 3: under 'Allow location'. Precise is iOS's name for precise location.")
    }

    static var keepOnCharge: String {
        String(localized: "Keep the phone on charge", comment: "Welcome, screen 3: the second thing a trip needs.")
    }

    static var keepOnChargeReason: String {
        String(localized: "The screen stays on while it charges", comment: "Welcome, screen 3: under 'Keep the phone on charge'.")
    }

    static var leaveAppOpen: String {
        String(localized: "Leave the app open", comment: "Welcome, screen 3: the third thing a trip needs.")
    }

    static var leaveAppOpenReason: String {
        String(localized: "If you close it, reports stop", comment: "Welcome, screen 3: under 'Leave the app open'.")
    }

    static var ifReportingStops: String {
        String(localized: "If reporting stops", comment: "Welcome, screen 3: heading over the card that shows what a stopped trip looks like.")
    }

    /// What a stopped trip says. Today that is the Resume screen's question;
    /// "Reports paused" comes with the Live Activity.
    static var stoppedTitle: String { ResumeText.title }

    static var stoppedDetail: String {
        String(localized: "Open the app and tap \(ResumeText.resumeTrip).", comment: "Welcome, screen 3: what to do when reporting has stopped. The name of the Resume screen's button.")
    }

    // MARK: VoiceOver

    /// A card or a row as one element: "Choose your vehicle. The number on
    /// the vehicle."
    static func spoken(title: String, detail: String) -> String {
        String(localized: "\(title). \(detail).", comment: "VoiceOver: a card or a row of the welcome in one sentence, its title then the line under it.")
    }

    static var spokenRidersSeeYou: String {
        String(localized: "\(ridersSeeYou).", comment: "VoiceOver: the welcome's green card as a sentence. The sent count beside it is not read.")
    }

    /// On the main actor, as the trip bar's words are.
    @MainActor static var spokenWhenYouFinish: String {
        String(localized: "\(whenYouFinish), \(TripStatusText.endTrip).", comment: "VoiceOver: the welcome's last row on screen 2, then the name of the End trip button it pictures.")
    }

    /// The stopped-trip card as one element. Its title is a question, so it
    /// brings its own stop.
    static var spokenStopped: String {
        String(localized: "\(stoppedTitle) \(stoppedDetail)", comment: "VoiceOver: the welcome's card for a stopped trip, its title then what to do.")
    }
}
