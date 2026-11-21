import SwiftUI

/// The account menu on every picker screen, `.toolbar { AccountMenu() }`:
/// who is signed in, to which server, which build, Help, and Sign out. There
/// is none on the tracking screen or in the car, so a driver ends the trip
/// first, then signs out, and Help never opens while the vehicle moves.
struct AccountMenu: ToolbarContent {
    var body: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) { AccountMenuButton() }
    }

    /// "Version 1.0.0 (1)", from the app's Info.plist.
    static func versionText(_ info: [String: Any]?) -> String {
        let (version, build) = versionAndBuild(info)
        return String(localized: "Version \(version) (\(build))", comment: "Account menu: the app's version, then its build number in brackets, e.g. 'Version 1.0.0 (7)'.")
    }

    /// "1.0.0 (1)": the same build where a label already says Version, as
    /// Help's About row and Share diagnostics do.
    nonisolated static func versionNumber(_ info: [String: Any]?) -> String {
        let (version, build) = versionAndBuild(info)
        return "\(version) (\(build))"
    }

    /// The server as the menu and Help name it: its host.
    static func serverText(_ url: URL?) -> String {
        url?.host() ?? ""
    }

    nonisolated private static func versionAndBuild(_ info: [String: Any]?) -> (version: String, build: String) {
        (info?["CFBundleShortVersionString"] as? String ?? "?", info?["CFBundleVersion"] as? String ?? "?")
    }

    /// The menu's groups, top to bottom, with a line between each: who is
    /// signed in, where and which build; Help, where it can open; Sign out.
    enum ItemGroup {
        case account, help, signOut
    }

    static func groups(offersHelp: Bool) -> [ItemGroup] {
        offersHelp ? [.account, .help, .signOut] : [.account, .signOut]
    }

    static var title: String {
        String(localized: "Account", comment: "VoiceOver label of the account menu's button on each picker screen. The menu shows the driver's email, the server and the app's version.")
    }

    static var signOut: String {
        String(localized: "Sign out", comment: "The account menu's item that signs the driver out, and the confirming button of the 'Sign out?' question it asks first.")
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

/// Sign out asks first, since the phone is usually about to go to the next
/// driver, and shows a spinner while the server is told.
private struct AccountMenuButton: View {
    @Environment(TripSession.self) private var session
    @Environment(\.openHelp) private var openHelp
    @State private var confirming = false

    var body: some View {
        Menu {
            ForEach(AccountMenu.groups(offersHelp: openHelp != nil), id: \.self) { group in
                switch group {
                case .account:
                    Section(session.settings.email) {
                        Label(AccountMenu.serverText(session.settings.serverURL), systemImage: "server.rack")
                        Label(AccountMenu.versionText(Bundle.main.infoDictionary), systemImage: "info.circle")
                    }
                case .help:
                    Section {
                        Button(HelpText.title, systemImage: "questionmark.circle") { openHelp?() }
                    }
                case .signOut:
                    Button(AccountMenu.signOut, systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
                        confirming = true
                    }
                    // A start from the car can be in flight while the phone
                    // shows a picker; a sign-out would do nothing until it is
                    // answered.
                    .disabled(session.phase.isStarting)
                }
            }
        } label: {
            if session.isSigningOut {
                ProgressView()
            } else {
                Label(AccountMenu.title, systemImage: "person.crop.circle")
            }
        }
        .disabled(session.isSigningOut)
        .confirmationDialog(AccountMenu.signOutTitle, isPresented: $confirming, titleVisibility: .visible) {
            Button(AccountMenu.signOut, role: .destructive) {
                Task { await session.signOut() }
            }
            Button(AccountMenu.cancel, role: .cancel) {}
        } message: {
            Text(AccountMenu.signOutMessage)
        }
    }
}
