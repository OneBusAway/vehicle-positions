import SwiftUI

extension EnvironmentValues {
    /// Pushes Settings on the pickers' stack. Set by `PickerFlow` only, so
    /// Settings opens from the pickers and nowhere else: never on Tracking,
    /// while the vehicle moves.
    @Entry var showSettings: ShowSettingsAction?
}

/// Opens Settings, as `dismiss` closes a screen. Any two are the same: each
/// pushes Settings on the one stack a view is in, so the button need not be
/// drawn again each time `PickerFlow` is.
struct ShowSettingsAction: Equatable {
    let show: () -> Void

    func callAsFunction() {
        show()
    }

    static func == (_: ShowSettingsAction, _: ShowSettingsAction) -> Bool {
        true
    }
}

/// The account button on every picker screen, `.toolbar { AccountButton() }`,
/// which opens Settings. There is none on the tracking screen or in the car,
/// so a driver ends the trip first, then signs out.
struct AccountButton: ToolbarContent {
    var body: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) { ShowSettingsButton() }
    }
}

/// Waits, with a spinner, while a sign-out asked for in Settings is told to
/// the server, and while a start is in flight, on the phone or from the car:
/// a sign-out would do nothing until it is answered.
private struct ShowSettingsButton: View {
    @Environment(TripSession.self) private var session
    @Environment(\.showSettings) private var showSettings

    var body: some View {
        Button { showSettings?() } label: {
            if session.isSigningOut {
                ProgressView()
            } else {
                Label(SettingsText.title, systemImage: "person.crop.circle")
            }
        }
        .disabled(session.isSigningOut || session.phase.isStarting)
    }
}

/// The words about who is signed in, where and with which build.
nonisolated enum AccountText {
    /// "1.0.0 (1)", from the app's Info.plist, as Settings and Share
    /// diagnostics name the build.
    static func versionNumber(_ info: [String: Any]?) -> String {
        "\(info?["CFBundleShortVersionString"] as? String ?? "?") (\(info?["CFBundleVersion"] as? String ?? "?"))"
    }

    /// The server as Settings names it: its host.
    static func serverText(_ url: URL?) -> String {
        url?.host() ?? ""
    }

    static var signOut: String {
        String(localized: "Sign out", comment: "The button in Settings that signs the driver out, and the confirming button of the 'Sign out?' question it asks first.")
    }

    static var signOutTitle: String {
        String(localized: "Sign out?", comment: "Asked before signing out, since the phone is usually going to the next driver.")
    }

    static var signOutMessage: String {
        String(localized: "The next driver can sign in on this phone.", comment: "Under 'Sign out?'.")
    }

    static var cancel: String {
        String(localized: "Cancel", comment: "Closes the 'Sign out?' question without signing out.")
    }
}
