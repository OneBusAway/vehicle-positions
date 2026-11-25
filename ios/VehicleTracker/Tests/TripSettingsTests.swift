import Foundation
import Testing
@testable import VehicleTracker

/// What the driver chose for Tracking: kept on the phone, what Tracking does
/// with it, and the words it goes by.
@Suite struct TripSettingsTests {
    func withDefaults(_ body: (UserDefaults) throws -> Void) rethrows {
        let name = "TripSettingsTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        try body(defaults)
    }

    // MARK: Kept

    /// What Tracking did before there was a choice.
    @Test func theDefaults() {
        withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            #expect(settings.screenOn == .charging)
            #expect(settings.mapUp == .heading)
            #expect(settings.vibratesOnProblem)
        }
    }

    @Test(arguments: ScreenOn.allCases) func whenTheScreenStaysOnIsKept(_ choice: ScreenOn) {
        withDefaults { defaults in
            AppSettings(defaults: defaults).screenOn = choice
            #expect(AppSettings(defaults: defaults).screenOn == choice)
        }
    }

    @Test(arguments: MapUp.allCases) func whatIsUpOnTheMapIsKept(_ choice: MapUp) {
        withDefaults { defaults in
            AppSettings(defaults: defaults).mapUp = choice
            #expect(AppSettings(defaults: defaults).mapUp == choice)
        }
    }

    @Test(arguments: [true, false]) func whetherItVibratesIsKept(_ on: Bool) {
        withDefaults { defaults in
            AppSettings(defaults: defaults).vibratesOnProblem = on
            #expect(AppSettings(defaults: defaults).vibratesOnProblem == on)
        }
    }

    @Test func theThreeAreKeptApart() {
        withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            settings.screenOn = .always
            settings.mapUp = .north
            settings.vibratesOnProblem = false

            let kept = AppSettings(defaults: defaults)
            #expect(kept.screenOn == .always)
            #expect(kept.mapUp == .north)
            #expect(!kept.vibratesOnProblem)
        }
    }

    /// As a later version might store, and this one be put back over.
    @Test func onesThisVersionDoesNotKnowAreTheDefaults() {
        withDefaults { defaults in
            defaults.set("moving", forKey: "screenOn")
            defaults.set("south", forKey: "mapUp")
            let settings = AppSettings(defaults: defaults)
            #expect(settings.screenOn == .charging)
            #expect(settings.mapUp == .heading)
        }
    }

    // MARK: The vibration

    nonisolated struct Change: Sendable, CustomTestStringConvertible {
        let from: TripStatus.Category
        let to: TripStatus.Category
        let vibrates: Bool
        let testDescription: String

        init(_ testDescription: String, _ from: TripStatus.Category, _ to: TripStatus.Category, vibrates: Bool) {
            self.testDescription = testDescription
            self.from = from
            self.to = to
            self.vibrates = vibrates
        }
    }

    nonisolated static func category(_ problem: TripStatus.Problem.Kind? = nil, signedOut: Bool = false,
                                     answer: TripStatus.Answer = .onTime) -> TripStatus.Category {
        TripStatus.Category(answer: answer, problem: problem, isSignedOut: signedOut)
    }

    nonisolated static let changes = [
        Change("a problem appears", category(), category(.noNetwork), vibrates: true),
        Change("one gives way to another", category(.noNetwork), category(.noGPS), vibrates: true),
        Change("one clears", category(.noNetwork), category(), vibrates: false),
        Change("one stays as the vehicle gets late", category(.noNetwork), category(.noNetwork, answer: .late), vibrates: false),
        Change("nothing but the answer changes", category(), category(answer: .offRoute), vibrates: false),
        Change("signed out", category(), category(signedOut: true), vibrates: true),
        Change("signed out in place of a problem", category(.noNetwork), category(signedOut: true), vibrates: true),
        Change("still signed out", category(signedOut: true), category(signedOut: true, answer: .late), vibrates: false),
        Change("signed in again", category(signedOut: true), category(), vibrates: false),
        Change("signed in again to a problem", category(signedOut: true), category(.noGPS), vibrates: true),
    ]

    @Test(arguments: changes) func thePhoneVibratesForWhatGetsWorse(_ change: Change) {
        #expect(change.to.vibrates(after: change.from, on: true) == change.vibrates)
    }

    @Test(arguments: changes) func offItVibratesForNothing(_ change: Change) {
        #expect(!change.to.vibrates(after: change.from, on: false))
    }

    // MARK: The words

    /// Android's, word for word.
    @Test func thePagesWords() {
        #expect(TripSettingsText.title == "During a trip")
        #expect(TripSettingsText.screen == "Screen")
        #expect(ScreenOn.allCases.map(TripSettingsText.name(of:)) == ["On while charging", "Always on"])
        #expect(ScreenOn.allCases.map(TripSettingsText.detail(of:)) == ["On battery it goes dark as usual", "Uses more battery"])
        #expect(TripSettingsText.map == "Map")
        #expect(MapUp.allCases.map(TripSettingsText.name(of:)) == ["The way you drive", "North up"])
        #expect(TripSettingsText.vibrate == "Vibrate when reporting has a problem")
        #expect(TripSettingsText.vibrateDetail == "No connection, no GPS, or signed out")
    }

    @Test func theRowSaysWhenTheScreenStaysOn() {
        #expect(ScreenOn.allCases.map(SettingsText.screen) == ["Screen on while charging", "Screen always on"])
    }
}
