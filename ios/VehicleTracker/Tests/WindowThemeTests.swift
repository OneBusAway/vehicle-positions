import Foundation
import Testing
import UIKit
@testable import VehicleTracker

/// The theme chosen in Settings: kept on the phone, and what the phone's
/// window is shown in.
@Suite struct WindowThemeTests {
    func withDefaults(_ body: (UserDefaults) throws -> Void) rethrows {
        let name = "WindowThemeTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        try body(defaults)
    }

    @Test func thePhonesOwnUntilOneIsChosen() {
        withDefaults { #expect(AppSettings(defaults: $0).theme == .system) }
    }

    @Test(arguments: ThemeChoice.allCases) func aChoiceIsKept(_ choice: ThemeChoice) {
        withDefaults { defaults in
            AppSettings(defaults: defaults).theme = choice
            #expect(AppSettings(defaults: defaults).theme == choice)
        }
    }

    /// As a later version might store, and this one be put back over.
    @Test func oneThisVersionDoesNotKnowIsThePhonesOwn() {
        withDefaults { defaults in
            defaults.set("sepia", forKey: "theme")
            #expect(AppSettings(defaults: defaults).theme == .system)
        }
    }

    @Test func eachChoiceIsItsStyle() {
        #expect(ThemeChoice.allCases.map(\.style) == [.unspecified, .light, .dark])
    }

    // MARK: The window

    @Test(arguments: ThemeChoice.allCases) func aWindowIsInTheThemeFromTheFirst(_ choice: ThemeChoice) {
        let window = UIWindow()
        window.addSubview(WindowThemeView(style: choice.style))
        #expect(window.overrideUserInterfaceStyle == choice.style)
    }

    @Test func aChangeOfChoiceIsAChangeOfTheWindow() {
        let window = UIWindow()
        let view = WindowThemeView(style: .unspecified)
        window.addSubview(view)

        view.style = .dark
        #expect(window.overrideUserInterfaceStyle == .dark)
        view.style = .unspecified
        #expect(window.overrideUserInterfaceStyle == .unspecified)
    }

    /// What comes up over the app comes up in its window.
    @Test func whatIsInTheWindowFollowsIt() {
        let window = UIWindow()
        let sheet = UIView()
        window.addSubview(sheet)
        window.addSubview(WindowThemeView(style: .dark))
        window.isHidden = false
        defer { window.isHidden = true }
        window.layoutIfNeeded()
        sheet.updateTraitsIfNeeded()
        #expect(sheet.traitCollection.userInterfaceStyle == .dark)
    }
}
