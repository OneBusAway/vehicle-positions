import Foundation
import SwiftUI
import Testing
@testable import VehicleTracker

/// The Language row on the first welcome screen, as Android's, and in
/// Settings: the language shown, named in that language.
@Suite struct LanguageRowTests {
    @Test func theRowNamesTheLanguageShown() {
        #expect(AppLanguage.shownCode == "en")
        #expect(LanguageText.row(AppLanguage.ownName(AppLanguage.shownCode)) == "Language: English")
    }

    /// In its own words and script, starting in upper case after "Language:"
    /// where the language writes its name in lower case.
    @Test(arguments: zip(["es", "fr", "pt-BR", "ar", "hi", "zh-Hans"],
                         ["Español", "Français", "Português (Brasil)", "العربية", "हिन्दी", "简体中文"]))
    func eachLanguageIsNamedInItself(code: String, name: String) {
        #expect(AppLanguage.ownName(code) == name)
    }

    /// iOS names every language the app has, so the row never shows a bare
    /// code such as "sm".
    @Test func everyLanguageOfTheAppHasAName() {
        let codes = Bundle.main.localizations.filter { $0 != "Base" }
        #expect(codes.count > 1)
        for code in codes {
            #expect(AppLanguage.ownName(code) != code, "\(code)")
        }
    }

    @Test func onlyTheFirstWelcomeScreenHasTheRow() {
        #expect(WelcomeStep.allCases.filter(\.hasLanguageRow) == [.riders])
    }

    /// A welcome screen takes taps only at rest in its place. One coming in
    /// is at `.identity` from the first frame of its move, so part of the way
    /// along its slide it takes none; one leaving takes none at all. The
    /// slide is Animatable, so SwiftUI carries its progress along frame by
    /// frame with the move, and reaches 0 only once the screen is there.
    @Test func aWelcomeScreenTakesTapsOnlyAtRestInItsPlace() {
        #expect(WelcomeStepTransition.takesTaps(.identity, progress: 0))
        #expect(!WelcomeStepTransition.takesTaps(.identity, progress: -0.4))
        #expect(!WelcomeStepTransition.takesTaps(.didDisappear, progress: 0))
        #expect(!WelcomeStepTransition.takesTaps(.didDisappear, progress: 0.6))
        let slide: Any.Type = WelcomeStepSlide.self
        #expect(slide is any Animatable.Type)
    }
}
