import SwiftUI

@main
struct VehicleTrackerApp: App {
    @State private var container = AppContainer()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(container.session)
        }
    }
}
