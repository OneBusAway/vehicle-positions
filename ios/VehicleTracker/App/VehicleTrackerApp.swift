import SwiftUI

@main
struct VehicleTrackerApp: App {
    private let container = AppContainer.shared

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(container.session)
                .environment(container.serverSetup)
                .environment(container.screenAwake)
                .task { DebugAutoStart.runIfRequested(session: container.session) }
                .onOpenURL { container.serverSetup.open($0) }
                .modifier(PhoneWindowCameToFront { container.liveActivity.sceneBecameActive() })
        }
    }
}

/// Runs `action` each time the phone app's window comes to the front. Read
/// inside the window, so the CarPlay scene coming up doesn't count:
/// ActivityKit starts a Live Activity only while the phone app is in front.
private struct PhoneWindowCameToFront: ViewModifier {
    @Environment(\.scenePhase) private var phase
    let action: () -> Void

    func body(content: Content) -> some View {
        content.onChange(of: phase) { _, phase in
            if phase == .active { action() }
        }
    }
}
