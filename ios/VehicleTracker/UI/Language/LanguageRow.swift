import SwiftUI

/// The Language row on the first welcome screen, as Android's: "Language:
/// English", in the language the app shows. iOS keeps each app's language on
/// the app's own page in Settings (UIPrefersShowingLanguageSettings), so a
/// tap opens that page. Its globe marks it for a driver who can't read the
/// words around it.
struct LanguageRow: View {
    @Environment(\.openURL) private var openURL

    var body: some View {
        Button { openURL(.appSettings) } label: {
            HStack(spacing: 14) {
                Image(systemName: "globe")
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Text(LanguageText.row(AppLanguage.ownName(AppLanguage.shownCode)))
                    .frame(maxWidth: .infinity, alignment: .leading)
                Image(systemName: "chevron.forward")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
        }
        .tint(.primary)
        .accessibilityHint(TripStatusText.opensSettings)
    }
}

/// The language the app is shown in, as the Language row names it.
nonisolated enum AppLanguage {
    /// The app's language iOS picked for its strings, from the app's own
    /// languages: "en", "es", "pt-BR".
    static var shownCode: String {
        Bundle.main.preferredLocalizations.first ?? "en"
    }

    /// `code`'s name in its own language and script, so a driver finds theirs
    /// without reading English. Some languages write it in lower case
    /// ("español"); after "Language:" it starts in upper case, as on Android.
    static func ownName(_ code: String) -> String {
        let locale = Locale(identifier: code)
        guard let name = locale.localizedString(forIdentifier: code), let first = name.first else { return code }
        return String(first).uppercased(with: locale) + name.dropFirst()
    }
}

nonisolated enum LanguageText {
    static func row(_ language: String) -> String {
        String(localized: "Language: \(language)", comment: "The Language row on the first welcome screen. The language the app is shown in, named in that language, e.g. 'Language: English'. A tap opens the app's page in Settings, where its language is chosen.")
    }
}
