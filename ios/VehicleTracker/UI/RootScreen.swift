/// The screen a trip's phase puts up (DESIGN-iOS §4.0, §4.8). Each screen
/// change goes through `next`, so the phone has one rule for it.
nonisolated enum RootScreen: Equatable {
    case signIn, welcome, pickers, resume, tracking

    /// The screen for `phase`, given the one `showing` now. An end in flight
    /// stays on the screen it was asked from: End trip on Resume would
    /// otherwise cross-fade to Tracking for the length of the request. A
    /// failed end puts the phase back, and its screen with it; a confirmed
    /// one goes on to the pickers.
    ///
    /// A driver who has not seen the welcome gets it before the pickers, and
    /// only there: a trip running or paused always shows first.
    static func next(phase: TripSession.Phase, showing: RootScreen, welcomeSeen: Bool) -> RootScreen {
        switch phase {
        case .signedOut: .signIn
        case .idle: welcomeSeen ? .pickers : .welcome
        // Only the car can start a run while the welcome shows. It stays up
        // until the server answers, rather than give way to the pickers for
        // the length of the request.
        case .starting: showing == .welcome ? .welcome : .pickers
        // A trip the app closed during waits for the driver to choose.
        case .paused: .resume
        case .active: .tracking
        case .ending: showing == .resume ? .resume : .tracking
        }
    }
}
