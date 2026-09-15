import SwiftUI

@main
struct VehicleTrackerApp: App {
    private let container = AppContainer.shared

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(container.session)
                .task { DebugAutoStart.runIfRequested(session: container.session) }
        }
    }
}
