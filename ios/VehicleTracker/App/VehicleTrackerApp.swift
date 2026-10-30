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
        }
    }
}
