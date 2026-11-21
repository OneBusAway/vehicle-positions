import SwiftUI

/// The alert a setup link leads to. Written once and applied in two places:
/// `RootView` outside Tracking, and the trip sheet inside it, because SwiftUI
/// cannot present an alert from under a sheet that is already up. Only one of
/// the two is `isActive` at a time, so they never both try to present it.
struct ServerSetupPrompt: ViewModifier {
    @Environment(ServerSetup.self) private var serverSetup
    let isActive: Bool

    func body(content: Content) -> some View {
        content.alert(title(of: serverSetup.prompt), isPresented: isPresented, presenting: serverSetup.prompt) { prompt in
            if case .confirm(let server, _) = prompt {
                Button(String(localized: "Use this server", comment: "Setup link alert: the button that fills in the link's server on the sign-in screen.")) { serverSetup.confirm(server) }
                Button(String(localized: "Cancel", comment: "Setup link alert: the button that closes it without using the link."), role: .cancel) { serverSetup.dismiss() }
            } else {
                Button(String(localized: "OK", comment: "Setup link alert: the button that closes it after the link was not used.")) { serverSetup.dismiss() }
            }
        } message: { prompt in
            if case .confirm = prompt {
                Text(String(localized: "Only continue if your agency sent you this link.", comment: "Setup link alert: under 'Use tracker.example.org?'."))
            }
        }
    }

    private var isPresented: Binding<Bool> {
        Binding(get: { isActive && serverSetup.prompt != nil }, set: { if !$0 { serverSetup.dismiss() } })
    }

    private func title(of prompt: ServerSetupLink.Decision?) -> String {
        switch prompt {
        case .confirm(_, let host):
            String(localized: "Use \(host)?", comment: "Title of the alert a setup link from the agency opens, which fills in the server on the sign-in screen once the driver agrees. The server's host, e.g. 'Use tracker.example.org?'.")
        case .refusedSignedIn(let host):
            String(localized: "This phone is signed in to \(host). The link was not used.", comment: "Title of the setup link alert when the phone is already signed in, so it keeps its server: the host of that server, e.g. 'This phone is signed in to tracker.example.org. The link was not used.'")
        case .refusedManaged:
            String(localized: "Your agency sets the server on this phone.", comment: "Title of the setup link alert when the agency's device management sets the server, so the link was not used.")
        case .invalid:
            String(localized: "This link does not contain a server address the app can use.", comment: "Title of the setup link alert when the link names no server address the app may use.")
        case nil:
            ""
        }
    }
}

extension View {
    /// Shows the setup link's alert here while `isActive`.
    func serverSetupPrompt(isActive: Bool = true) -> some View {
        modifier(ServerSetupPrompt(isActive: isActive))
    }
}
