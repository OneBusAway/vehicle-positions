import SwiftUI

struct RootView: View {
    @Environment(TripSession.self) private var session
    /// Kept from the first phase change on, so an end in flight can stay on
    /// the screen it came from.
    @State private var screen: RootScreen?

    /// Until the first change, the launch phase decides: the first frame is
    /// already the right screen, and a relaunch onto Resume never shows
    /// Sign in first.
    private var shown: RootScreen {
        screen ?? RootScreen.next(phase: session.phase, showing: .signIn)
    }

    var body: some View {
        ZStack {
            switch shown {
            case .signIn:
                SignInView().transition(.opacity)
            case .pickers:
                PickerFlow().transition(.opacity)
            case .resume:
                ResumeView().transition(.opacity)
            case .tracking:
                TrackingView().transition(.opacity)
            }
        }
        .animation(.smooth(duration: 0.35), value: shown)
        .onChange(of: session.phase, initial: true) { _, phase in
            screen = RootScreen.next(phase: phase, showing: shown)
        }
        // The trip started, or ended, on the server (DESIGN-iOS §2.7).
        .sensoryFeedback(.success, trigger: session.phase) { old, new in
            switch (old, new) {
            case (.starting, .active), (.ending, .idle): true
            default: false
            }
        }
        // Inside Tracking the trip sheet shows it: an alert cannot present
        // from under a sheet that is already up.
        .serverSetupPrompt(isActive: shown != .tracking)
    }
}
