import Foundation

/// Every word Settings shows of its own, in Android's words (`settings_*`).
nonisolated enum SettingsText {
    static var title: String {
        String(localized: "Settings", comment: "The title of the app's own Settings page, and the VoiceOver label of the account button on each picker screen, which opens it.")
    }

    // MARK: The header

    static var ready: String {
        String(localized: "Ready to report", comment: "Settings, large at the top: this phone can report a trip.")
    }

    static var notReady: String {
        String(localized: "Not ready to report", comment: "Settings, large at the top: this phone can't report a trip yet.")
    }

    static var readyBody: String {
        String(localized: "Start a trip and riders see your vehicle.", comment: "Settings, under 'Ready to report'.")
    }

    static var notAsked: String {
        String(localized: "The app asks for location when you start a trip.", comment: "Settings, under 'Not ready to report', when the app has not asked for location yet.")
    }

    static var refused: String {
        String(localized: "Location is not allowed for this app.", comment: "Settings, under 'Not ready to report', when location was refused to the app.")
    }

    static var restricted: String {
        String(localized: "This iPhone does not let apps use location.", comment: "Settings, under 'Not ready to report', when parental controls or the agency's management of the iPhone forbid location.")
    }

    static var preciseOff: String {
        String(localized: "Precise location is off for this app.", comment: "Settings, under 'Not ready to report', when the app has only approximate location.")
    }

    static var servicesOff: String {
        String(localized: "Location is off for the whole phone.", comment: "Settings, under 'Not ready to report', when Location Services are off.")
    }

    // MARK: The rows

    static var appSection: String {
        String(localized: "App", comment: "Settings: the heading over the rows about the app itself, such as its language.")
    }

    static var language: String {
        String(localized: "Language", comment: "Settings: the row that names the language the app is shown in, at its end. A tap opens the app's page in the Settings app, where its language is chosen.")
    }

    static var theme: String {
        String(localized: "Theme", comment: "Settings: the row that names the theme the app is shown in, at its end. A tap opens a menu of the themes.")
    }

    static func name(of theme: ThemeChoice) -> String {
        switch theme {
        case .system: String(localized: "System", comment: "Settings, a theme: the app is light or dark as the phone is.")
        case .light: String(localized: "Light", comment: "Settings, a theme: the app is light whatever the phone is.")
        case .dark: String(localized: "Dark", comment: "Settings, a theme: the app is dark whatever the phone is.")
        }
    }

    static var aboutSection: String {
        String(localized: "About", comment: "Settings: the heading over Help, Share diagnostics and the app's version.")
    }

    static var version: String {
        String(localized: "Version", comment: "Settings, About: the label beside the app's version, e.g. '1.0.0 (1)'.")
    }

    static var shareDiagnostics: String {
        String(localized: "Share diagnostics", comment: "Settings: opens the share sheet with a small text file about the app and the iPhone, for whoever looks after the app.")
    }

    static var shareDiagnosticsDetail: String {
        String(localized: "Sends the app version, the iPhone model, the server, what the app is allowed to do and recent errors. No positions, no sign-in details, no email.", comment: "Settings: under the group with 'Share diagnostics', what its file holds and what it never holds.")
    }
}
