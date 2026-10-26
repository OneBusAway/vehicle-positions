import SwiftUI

/// The account menu on every picker screen, `.toolbar { AccountMenu() }`:
/// who is signed in, to which server, which build, and Sign out. There is
/// none on the tracking screen or in the car, so a driver ends the trip
/// first, then signs out.
struct AccountMenu: ToolbarContent {
    var body: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) { AccountMenuButton() }
    }

    /// "Version 1.0.0 (1)", from the app's Info.plist.
    static func versionText(_ info: [String: Any]?) -> String {
        let version = info?["CFBundleShortVersionString"] as? String ?? "?"
        let build = info?["CFBundleVersion"] as? String ?? "?"
        return String(localized: "Version \(version) (\(build))")
    }
}

/// Sign out asks first, since the phone is usually about to go to the next
/// driver, and shows a spinner while the server is told.
private struct AccountMenuButton: View {
    @Environment(TripSession.self) private var session
    @State private var confirming = false
    @State private var signingOut = false

    var body: some View {
        Menu {
            Section(session.settings.email) {
                Label(session.settings.serverURL?.host() ?? "", systemImage: "server.rack")
                Label(AccountMenu.versionText(Bundle.main.infoDictionary), systemImage: "info.circle")
            }
            Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
                confirming = true
            }
            // A start from the car can be in flight while the phone shows a
            // picker; a sign-out would do nothing until it is answered.
            .disabled(session.phase.isStarting)
        } label: {
            if signingOut {
                ProgressView()
            } else {
                Label("Account", systemImage: "person.crop.circle")
            }
        }
        .disabled(signingOut)
        .confirmationDialog("Sign out?", isPresented: $confirming, titleVisibility: .visible) {
            Button("Sign out", role: .destructive) {
                signingOut = true
                Task {
                    await session.signOut()
                    signingOut = false
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The next driver can sign in on this phone.")
        }
    }
}
