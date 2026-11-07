/// The screen a trip's phase puts up (DESIGN-iOS §4.0, §4.8). Each screen
/// change goes through `next`, so the phone has one rule for it.
nonisolated enum RootScreen: Equatable {
    case signIn, pickers, resume, tracking

    /// The screen for `phase`, given the one `showing` now. An end in flight
    /// stays on the screen it was asked from: End trip on Resume would
    /// otherwise cross-fade to Tracking for the length of the request. A
    /// failed end puts the phase back, and its screen with it; a confirmed
    /// one goes on to the pickers.
    static func next(phase: TripSession.Phase, showing: RootScreen) -> RootScreen {
        switch phase {
        case .signedOut: .signIn
        case .idle, .starting: .pickers
        // A trip the app closed during waits for the driver to choose.
        case .paused: .resume
        case .active: .tracking
        case .ending: showing == .resume ? .resume : .tracking
        }
    }
}
