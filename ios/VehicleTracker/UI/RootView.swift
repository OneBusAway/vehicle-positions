import SwiftUI

struct RootView: View {
    @Environment(TripSession.self) private var session
    @Environment(ServerSetup.self) private var serverSetup

    var body: some View {
        Group {
            switch session.phase {
            case .signedOut:
                LoginView()
            case .idle, .starting:
                PickerFlow()
            case .active, .paused, .ending:
                TrackingView()
            }
        }
        .alert(title(of: serverSetup.prompt), isPresented: isPrompting, presenting: serverSetup.prompt) { prompt in
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

    private var isPrompting: Binding<Bool> {
        Binding(get: { serverSetup.prompt != nil }, set: { if !$0 { serverSetup.dismiss() } })
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
