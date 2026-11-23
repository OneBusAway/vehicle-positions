import Foundation
import Testing
@testable import VehicleTracker

/// The Language row on the first welcome screen and at the top of Help, as
/// Android's: the language shown, named in that language.
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

    /// A welcome screen sliding in or fading out takes no taps, so its
    /// Language row can't be hit before the screen is shown.
    @Test func aWelcomeScreenTakesTapsOnlyOnceInPlace() {
        #expect(WelcomeStepTransition.takesTaps(.identity))
        #expect(!WelcomeStepTransition.takesTaps(.willAppear))
        #expect(!WelcomeStepTransition.takesTaps(.didDisappear))
    }
}
