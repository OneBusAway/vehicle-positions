import SwiftUI

/// One answer the status card on Tracking gives, or its line for the reports
/// waiting on the phone, as Help explains it: the card's own words, symbol
/// and colour, then one plain line. In the order Help lists them
/// (walkthrough spec §9, then Android's waiting row).
///
/// iOS has no "No schedule" answer: a trip with no stops is refused at start
/// and ended on resume (`TripStatus.waiting`), so Help has no row for it.
enum StatusHelp: CaseIterable {
    case onTime, late, early, offSchedule, offRoute, waitingForGPS, reportsWaiting

    /// The row for the card's answer. A new answer does not compile until it
    /// has one.
    init(_ answer: TripStatus.Answer) {
        switch answer {
        case .onTime: self = .onTime
        case .late: self = .late
        case .early: self = .early
        case .offSchedule: self = .offSchedule
        case .offRoute: self = .offRoute
        case .waiting: self = .waitingForGPS
        }
    }

    /// What the card says, in its own words: "6 min late" and "3 min early"
    /// as examples of the minutes, "Off route" without the distance, as
    /// VoiceOver and the stop list say it, and 3 reports waiting, as
    /// Android's Help shows.
    var title: String {
        switch self {
        case .onTime: TripStatusText.onTime
        case .late: TripStatusText.minutesLate(6)
        case .early: TripStatusText.minutesEarly(3)
        case .offSchedule: TripStatusText.offSchedule
        case .offRoute: TripStatusText.offRouteShort
        case .waitingForGPS: TripStatusText.waitingForGPS
        case .reportsWaiting: TripStatusText.reportsWaiting(3)
        }
    }

    var meaning: String {
        switch self {
        case .onTime: HelpText.onTimeMeaning
        case .late: HelpText.lateMeaning
        case .early: HelpText.earlyMeaning
        case .offSchedule: HelpText.offScheduleMeaning
        case .offRoute: HelpText.offRouteMeaning
        case .waitingForGPS: HelpText.waitingForGPSMeaning
        case .reportsWaiting: HelpText.reportsWaitingMeaning
        }
    }

    /// The card's symbol for this answer. `TripStatus` keeps its own to
    /// itself; `HelpRowsTests` checks the two are the same. The waiting line
    /// has none on the card, so its row has Android's clock.
    var symbol: String {
        switch self {
        case .onTime: "checkmark.circle.fill"
        case .late: "hourglass.bottomhalf.filled"
        case .early: "gauge.with.dots.needle.67percent"
        case .offSchedule: "calendar.badge.exclamationmark"
        case .offRoute: "mappin.slash"
        case .waitingForGPS: "location.magnifyingglass"
        case .reportsWaiting: "clock"
        }
    }

    /// The card's colour. Off schedule is red or blue by which way the run is
    /// off; Help shows the red of a run started too early, as Android's does.
    /// The waiting line takes the answer's colour on the card, and Help shows
    /// it grey, as Android's does.
    var tone: StatusTone {
        switch self {
        case .onTime: .onTime
        case .late: .late
        case .early, .offSchedule: .early
        case .offRoute, .waitingForGPS, .reportsWaiting: .neutral
        }
    }
}

/// One line that can show under the status card, or the Signed out card that
/// takes the card's place, as Help explains it: the line's own words and
/// symbol, then one plain line. In the order Help lists them (walkthrough
/// spec §9, the iOS table).
enum ProblemHelp: CaseIterable {
    case noConnection, noGPS, locationOff, preciseLocationOff, locationStopped, clockWrong, refused, serverError,
         signedOut, openOnIPhone

    /// The row for a problem line. A new kind does not compile until it has
    /// one.
    init(_ kind: TripStatus.Problem.Kind) {
        switch kind {
        case .noNetwork: self = .noConnection
        case .noGPS: self = .noGPS
        case .locationDenied: self = .locationOff
        case .preciseLocationOff: self = .preciseLocationOff
        case .locationStopped: self = .locationStopped
        case .clockSkew: self = .clockWrong
        case .refused: self = .refused
        case .serverError: self = .serverError
        case .needsForeground: self = .openOnIPhone
        }
    }

    /// The row for what shows under the card, or in its place: the Signed
    /// out card, or the problem line. Nil while all is well.
    init?(_ status: TripStatus) {
        if status.isSignedOut {
            self = .signedOut
        } else if let problem = status.problem {
            self.init(problem.kind)
        } else {
            return nil
        }
    }

    var title: String {
        switch self {
        case .noConnection: TripStatusText.noConnection
        case .noGPS: TripStatusText.noGPSSignal
        case .locationOff: TripStatusText.locationOff
        case .preciseLocationOff: TripStatusText.preciseLocationOff
        case .locationStopped: TripStatusText.locationStopped
        case .clockWrong: TripStatusText.phoneClockWrong
        case .refused: TripStatusText.reportsRefused
        case .serverError: TripStatusText.serverError
        case .signedOut: TripStatusText.signedOut
        case .openOnIPhone: TripStatusText.openAppOnIPhone
        }
    }

    var meaning: String {
        switch self {
        case .noConnection: HelpText.noConnectionMeaning
        case .noGPS: HelpText.noGPSMeaning
        case .locationOff, .preciseLocationOff: HelpText.locationOffMeaning
        case .locationStopped: HelpText.locationStoppedMeaning
        case .clockWrong: HelpText.clockWrongMeaning
        case .refused: HelpText.refusedMeaning
        case .serverError: HelpText.serverErrorMeaning
        case .signedOut: HelpText.signedOutMeaning
        case .openOnIPhone: HelpText.openOnIPhoneMeaning
        }
    }

    /// The line's symbol, or the Signed out card's lock. `TripStatus` keeps
    /// its own to itself; `HelpRowsTests` checks the two are the same.
    var symbol: String {
        switch self {
        case .noConnection: "icloud.slash.fill"
        case .noGPS, .locationOff, .locationStopped: "location.slash.fill"
        case .preciseLocationOff: "scope"
        case .clockWrong: "clock.badge.exclamationmark"
        case .refused: "hand.raised.fill"
        case .serverError: "exclamationmark.icloud.fill"
        case .signedOut: "lock.fill"
        case .openOnIPhone: "iphone"
        }
    }
}

/// One explained row of Help: a symbol on its disc, the words Tracking
/// shows, and what they mean.
struct HelpEntry: Identifiable, Equatable {
    let id: String
    let title: String
    let meaning: String
    let symbol: String
    let disc: Color
    let ink: Color
}

/// The rows of Help's two explained groups, worked out once: nothing on the
/// page changes while it shows, so scrolling only draws them.
enum HelpPage {
    static let statusEntries: [HelpEntry] = StatusHelp.allCases.map { help in
        HelpEntry(id: "status-\(help)", title: help.title, meaning: help.meaning, symbol: help.symbol,
                  disc: help.tone.fill, ink: .onStatusFill)
    }

    static let problemEntries: [HelpEntry] = ProblemHelp.allCases.map { help in
        HelpEntry(id: "problem-\(help)", title: help.title, meaning: help.meaning, symbol: help.symbol,
                  disc: .attentionContainer, ink: .amberOnSurface)
    }
}
