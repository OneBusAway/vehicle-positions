import CoreGraphics

/// The header of Settings: whether this phone can report a trip, and the line
/// under it. The line has a stop for each row of Before you start, in its
/// order, between the phone and the riders. Android's `Readiness`.
nonisolated struct Readiness: Equatable {
    enum State: Equatable {
        /// A trip can start. `waiting` is the reports kept on the phone.
        case ready(waiting: Int)
        case notAsked, refused, restricted, preciseOff, servicesOff
    }

    /// How a row's stop is drawn on the line.
    enum Stop: Equatable {
        case allowed, missing
        /// Nothing is known of it, or there is nothing to fix there yet.
        case beyond
    }

    let state: State

    /// What stops a trip is what stops Before you start.
    init(access: LocationAccessState, waiting: Int) {
        state = switch access {
        case .allowed: .ready(waiting: waiting)
        case .notDetermined: .notAsked
        case .denied: .refused
        case .restricted: .restricted
        case .approximateOnly: .preciseOff
        case .servicesOff: .servicesOff
        }
    }

    var canReport: Bool {
        if case .ready = state { return true }
        return false
    }

    /// Whether the app's page in Settings can fix it. iOS has nothing there
    /// about location until the app has asked, and nothing can lift a
    /// restriction.
    var opensSettings: Bool {
        switch state {
        case .refused, .preciseOff, .servicesOff: true
        case .ready, .notAsked, .restricted: false
        }
    }

    var stops: [Stop] {
        LocationChecklist.Row.allCases.map(stop(for:))
    }

    private func stop(for row: LocationChecklist.Row) -> Stop {
        switch (state, row) {
        case (.ready, _): .allowed
        case (.preciseOff, .precise): .missing
        case (.preciseOff, _): .allowed
        // With Location Services off, iOS reads the app as refused, so
        // nothing is known about the rows above it.
        case (.servicesOff, .locationServices): .missing
        case (.servicesOff, _): .beyond
        case (_, .location): .missing
        // iOS says whether Location Services are on only to an app it would
        // let use location.
        case (.restricted, .locationServices): .beyond
        case (_, .locationServices): .allowed
        case (_, .precise): .beyond
        }
    }

    /// Where the vehicle stands: 0 before the first stop, n on the nth, and
    /// one past the last by the riders. It waits on the stop before the first
    /// it can't get past.
    var vehicleAt: Int {
        canReport ? stops.count + 1 : stops.prefix { $0 == .allowed }.count
    }
}

/// What the header says, in words.
struct ReadinessWords: Hashable {
    let title: String
    let sentence: String
    /// Beside the sentence, when it is more than the title explained.
    var symbol: String?

    init(_ readiness: Readiness) {
        title = readiness.canReport ? SettingsText.ready : SettingsText.notReady
        switch readiness.state {
        case .ready(waiting: 0):
            sentence = SettingsText.readyBody
        case .ready(let waiting):
            sentence = TripStatusText.reportsWaiting(waiting)
            symbol = "clock"
        case .notAsked: sentence = SettingsText.notAsked
        case .refused: sentence = SettingsText.refused
        case .restricted: sentence = SettingsText.restricted
        case .preciseOff: sentence = SettingsText.preciseOff
        case .servicesOff: sentence = SettingsText.servicesOff
        }
    }
}

/// Where things stand along a line `width` wide, from its leading edge.
nonisolated struct LineGeometry: Equatable {
    /// Under this width the stops are closer, so the vehicle is smaller and
    /// the state's words a size down.
    static let narrowWidth: CGFloat = 350
    private static let riderInset: CGFloat = 34
    private static let vehicleFromRider: CGFloat = 42
    private static let closestIcons: CGFloat = 32

    let width: CGFloat
    let stops: Int

    var isNarrow: Bool { width < Self.narrowWidth }
    var rider: CGFloat { width - Self.riderInset }
    private var home: CGFloat { isNarrow ? 30 : 36 }
    private var spacing: CGFloat { (rider - Self.vehicleFromRider - home) / CGFloat(stops + 1) }

    /// `position` as `Readiness.vehicleAt` counts.
    func x(at position: Double) -> CGFloat { home + spacing * position }

    /// Whether a row's symbol fits over each stop.
    var hasRoomForSymbols: Bool { spacing >= Self.closestIcons }
}

/// The header's fold: as the page scrolls the header moves up with it until
/// only its bar and its line are left, then stays.
nonisolated struct HeaderFold: Equatable {
    /// How far the header moves before it stays.
    var distance: CGFloat = 0

    /// How far it has folded, 0 to 1, with the page scrolled by `scroll`.
    func fraction(_ scroll: CGFloat) -> CGFloat {
        distance > 0 ? min(max(scroll / distance, 0), 1) : 0
    }

    /// How far past the fold the page is, which the header stays put against.
    func pastFold(_ scroll: CGFloat) -> CGFloat { max(scroll - distance, 0) }

    /// Where a page let go to come to rest at `scroll` rests instead: in the
    /// middle of the fold, the nearer end. A page that can scroll no further
    /// than `furthest` may be too short to fold it all, and is left alone.
    func settled(_ scroll: CGFloat, furthest: CGFloat) -> CGFloat {
        guard scroll > 0, scroll < distance, distance <= furthest else { return scroll }
        return scroll * 2 < distance ? 0 : distance
    }

    /// A part of the fold, as it fades or grows between `from` and `to` of it.
    static func within(_ fraction: CGFloat, _ from: CGFloat, _ to: CGFloat) -> CGFloat {
        min(max((fraction - from) / (to - from), 0), 1)
    }
}
