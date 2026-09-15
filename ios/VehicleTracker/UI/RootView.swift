import SwiftUI

struct RootView: View {
    @Environment(TripSession.self) private var session

    var body: some View {
        switch session.phase {
        case .signedOut:
            LoginView()
        case .idle, .starting:
            PickerFlow()
        case .active, .paused, .ending:
            TrackingView()
        }
    }
}
