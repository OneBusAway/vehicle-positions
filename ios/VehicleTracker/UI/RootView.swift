import SwiftUI

struct RootView: View {
    @Environment(TripSession.self) private var session

    /// Which screen the phase puts up. `.active`, `.paused` and `.ending` are
    /// all Tracking, so a trip pausing or ending does not cross-fade the
    /// screen with itself.
    private enum Screen: Equatable {
        case signIn, pickers, tracking
    }

    private var screen: Screen {
        switch session.phase {
        case .signedOut: .signIn
        case .idle, .starting: .pickers
        case .active, .paused, .ending: .tracking
        }
    }

    var body: some View {
        ZStack {
            switch screen {
            case .signIn:
                SignInView().transition(.opacity)
            case .pickers:
                PickerFlow().transition(.opacity)
            case .tracking:
                TrackingView().transition(.opacity)
            }
        }
        .animation(.smooth(duration: 0.35), value: screen)
        // The trip started, or ended, on the server (DESIGN-iOS §2.7).
        .sensoryFeedback(.success, trigger: session.phase) { old, new in
            switch (old, new) {
            case (.starting, .active), (.ending, .idle): true
            default: false
            }
        }
        // Inside Tracking the trip sheet shows it: an alert cannot present
        // from under a sheet that is already up.
        .serverSetupPrompt(isActive: screen != .tracking)
    }
}
