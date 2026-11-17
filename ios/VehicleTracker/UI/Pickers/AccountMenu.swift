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
        return String(localized: "Version \(version) (\(build))")
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
                    Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
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
                Label("Account", systemImage: "person.crop.circle")
            }
        }
        .disabled(session.isSigningOut)
        .confirmationDialog("Sign out?", isPresented: $confirming, titleVisibility: .visible) {
            Button("Sign out", role: .destructive) {
                Task { await session.signOut() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The next driver can sign in on this phone.")
        }
    }
}
