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
                Button("Use this server") { serverSetup.confirm(server) }
                Button("Cancel", role: .cancel) { serverSetup.dismiss() }
            } else {
                Button("OK") { serverSetup.dismiss() }
            }
        } message: { prompt in
            if case .confirm = prompt {
                Text("Only continue if your agency sent you this link.")
            }
        }
    }

    private var isPresented: Binding<Bool> {
        Binding(get: { isActive && serverSetup.prompt != nil }, set: { if !$0 { serverSetup.dismiss() } })
    }

    private func title(of prompt: ServerSetupLink.Decision?) -> Text {
        switch prompt {
        case .confirm(_, let host): Text("Use \(host)?")
        case .refusedSignedIn(let host): Text("This phone is signed in to \(host). The link was not used.")
        case .refusedManaged: Text("Your agency sets the server on this phone.")
        case .invalid: Text("This link does not contain a server address the app can use.")
        case nil: Text(verbatim: "")
        }
    }
}

extension View {
    /// Shows the setup link's alert here while `isActive`.
    func serverSetupPrompt(isActive: Bool = true) -> some View {
        modifier(ServerSetupPrompt(isActive: isActive))
    }
}
