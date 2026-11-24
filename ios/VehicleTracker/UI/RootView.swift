import SwiftUI

struct RootView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.scenePhase) private var scenePhase
    /// Kept from the first phase change on, so an end in flight can stay on
    /// the screen it came from.
    @State private var screen: RootScreen?

    /// Until the first change, the launch phase decides: the first frame is
    /// already the right screen, and a relaunch onto Resume never shows
    /// Sign in first.
    private var shown: RootScreen {
        screen ?? RootScreen.next(phase: session.phase, showing: .signIn, welcomeSeen: welcomeSeen,
                                  viewerSignedOut: session.viewerSignedOut)
    }

    private var welcomeSeen: Bool { session.settings.hasSeen(.welcome) }

    var body: some View {
        ZStack {
            switch shown {
            case .signIn:
                SignInView().transition(.opacity)
            case .welcome:
                WelcomeView { session.settings.markSeen(.welcome) }.transition(.opacity)
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
            screen = RootScreen.next(phase: phase, showing: shown, welcomeSeen: welcomeSeen,
                                     viewerSignedOut: session.viewerSignedOut)
        }
        // Skip, or Continue on the welcome's last screen.
        .onChange(of: welcomeSeen) { _, seen in
            screen = RootScreen.next(phase: session.phase, showing: shown, welcomeSeen: seen,
                                     viewerSignedOut: session.viewerSignedOut)
        }
        // Sign in, on the vehicle screen that told a viewer it can't drive.
        .onChange(of: session.viewerSignedOut) { _, viewerSignedOut in
            screen = RootScreen.next(phase: session.phase, showing: shown, welcomeSeen: welcomeSeen,
                                     viewerSignedOut: viewerSignedOut)
        }
        // Initial too: a launch can come up already in front.
        .onChange(of: scenePhase, initial: true) { _, phase in
            if phase == .active { session.sendKeptReports() }
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
