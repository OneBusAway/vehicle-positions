import Foundation

/// What Before you start shows and what its Continue does, from what iOS
/// says now and whether the sheet has asked for precise location yet. iOS's
/// form of Android's `PermissionSteps`: it has no background, notification
/// or battery steps, and it never prompts twice for location, so a refusal
/// is final at once and there is nothing more to remember.
nonisolated struct LocationChecklist: Equatable {
    /// The rows, in Android's order. Location services stays last: on both
    /// apps it is reached only once location itself is settled.
    enum Row: CaseIterable {
        case location, precise, locationServices
    }

    enum RowState: Equatable {
        /// Continue asks iOS about this one next.
        case next
        case allowed
        /// Refused, and iOS won't ask again: only Settings can change it.
        case neededInSettings
        /// Precise location was declined after the sheet asked; Try again asks again.
        case approximate
        /// Location Services are off for the whole phone; Try again reads them again.
        case off
        case notReached

        /// The row's own action, if it has one, which Cancel can sit beside.
        var action: RowAction? {
            switch self {
            case .neededInSettings: .openSettings
            case .approximate: .askPrecise
            case .off: .readAgain
            case .next, .allowed, .notReached: nil
            }
        }
    }

    enum RowAction: Equatable {
        case openSettings, askPrecise, readAgain
    }

    /// The system prompt Continue raises.
    enum Ask: Equatable {
        case location, precise
    }

    private(set) var access: LocationAccessState
    /// The sheet asked for precise location since it opened, so iOS's answer
    /// stands: Continue doesn't ask again, the row's Try again does.
    private(set) var preciseAsked = false

    init(_ access: LocationAccessState) {
        self.access = access
    }

    /// Whether a run needs the sheet before it starts.
    static func needed(_ access: LocationAccessState) -> Bool {
        access != .allowed
    }

    /// What iOS says now: after an answer to the location prompt, on coming
    /// back from Settings, or when Try again reads Location Services again.
    mutating func read(_ access: LocationAccessState) {
        self.access = access
    }

    /// What iOS says after the precise-location prompt.
    mutating func answeredPrecise(_ access: LocationAccessState) {
        self.access = access
        preciseAsked = true
    }

    func state(of row: Row) -> RowState {
        switch (access, row) {
        case (.allowed, _): .allowed
        case (.notDetermined, .location): .next
        case (.approximateOnly, .location): .allowed
        case (.approximateOnly, .precise): preciseAsked ? .approximate : .next
        case (.denied, .location), (.restricted, .location): .neededInSettings
        // With Location Services off, iOS reads the app as refused, so
        // nothing is known about the rows above it.
        case (.servicesOff, .locationServices): .off
        default: .notReached
        }
    }

    /// The prompt Continue raises, or nil when it has nothing to ask: a row
    /// must be acted on first.
    var continueAsk: Ask? {
        if state(of: .location) == .next { return .location }
        if state(of: .precise) == .next { return .precise }
        return nil
    }

    /// The run can start: the sheet closes by itself.
    var isReady: Bool { access == .allowed }

    /// Cancel shows only once nothing left on the sheet raises a system
    /// prompt by itself. App Review rejects a screen before the permission
    /// prompt that can be left without reaching it (guideline 5.1.1(iv);
    /// OneBusAway iOS 24.3 and 26.4 were rejected for it), so while Continue
    /// would ask, the driver answers in iOS's own prompt instead.
    var canCancel: Bool { continueAsk == nil && !isReady }
}
