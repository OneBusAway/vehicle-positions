import Foundation

/// The welcome's three screens, in order.
nonisolated enum WelcomeStep: Int, CaseIterable {
    case riders, choices, reporting

    var title: String {
        switch self {
        case .riders: WelcomeText.ridersTitle
        case .choices: WelcomeText.choicesTitle
        case .reporting: WelcomeText.reportingTitle
        }
    }

    var sentence: String {
        switch self {
        case .riders: WelcomeText.ridersSentence
        case .choices: WelcomeText.choicesSentence
        case .reporting: WelcomeText.reportingSentence
        }
    }

    /// The last screen's button ends the welcome rather than going on.
    var button: String {
        self == Self.allCases.last ? WelcomeText.continue : WelcomeText.next
    }

    /// The first screen has the Language row under its sentence, as
    /// Android's does, for a driver who can't read the language shown.
    var hasLanguageRow: Bool {
        self == Self.allCases.first
    }

    /// "Step 1 of 3": the route line across the top, for VoiceOver.
    var spokenStep: String {
        WelcomeText.step(rawValue + 1, of: Self.allCases.count)
    }

    /// The screen's picture from its first frame to its rest, with the time
    /// each beat falls at.
    var timeline: [(at: Duration, picture: WelcomePicture)] {
        switch self {
        case .riders: RidersBeat.allCases.map { ($0.at, .riders($0)) }
        case .choices: ChoicesBeat.allCases.map { ($0.at, .choices($0)) }
        case .reporting: ReportingBeat.allCases.map { ($0.at, .reporting($0)) }
        }
    }
}

/// A screen's picture and how far it has played.
nonisolated enum WelcomePicture: Equatable {
    case riders(RidersBeat)
    case choices(ChoicesBeat)
    case reporting(ReportingBeat)

    var step: WelcomeStep {
        switch self {
        case .riders: .riders
        case .choices: .choices
        case .reporting: .reporting
        }
    }
}

/// A moment in a picture. Each case is one thing happening, and all that
/// happened before it stays: a picture asks whether a beat has been reached,
/// never which one is playing. The times say when a thing starts; a spring
/// decides how it settles.
nonisolated protocol WelcomeBeat: CaseIterable, Comparable, RawRepresentable where RawValue == Int {
    /// Counted from the moment the screen shows.
    var at: Duration { get }
}

nonisolated extension WelcomeBeat {
    static func < (lhs: Self, rhs: Self) -> Bool {
        lhs.rawValue < rhs.rawValue
    }

    /// How many of `beats` have been reached.
    func reached(_ beats: Self...) -> Int {
        beats.count { self >= $0 }
    }
}

/// Screen 1: the vehicle goes live and glides up its route, the riders at
/// the next stop see it coming, and the reports go out.
nonisolated enum RidersBeat: Int, WelcomeBeat {
    /// The vehicle waits at the bottom in a grey ring; nobody sees it.
    case start
    /// The ring turns green, the vehicle sets off, it shows on the rider's
    /// phone, and the first ring leaves it.
    case live
    case secondRing
    /// The riders' chip lights up.
    case ridersNotice
    case firstWave
    /// The first report is sent, as the third ring leaves.
    case firstReport
    case secondWave
    case thirdWave
    case secondReport
    case thirdReport
    /// Two still rings stay round the vehicle.
    case rest

    var at: Duration {
        switch self {
        case .start: .zero
        case .live: .milliseconds(500)
        case .secondRing: .milliseconds(1000)
        case .ridersNotice: .milliseconds(1200)
        case .firstWave: .milliseconds(1300)
        case .firstReport: .milliseconds(1500)
        case .secondWave: .milliseconds(1620)
        case .thirdWave: .milliseconds(1940)
        case .secondReport: .milliseconds(2200)
        case .thirdReport: .milliseconds(2900)
        case .rest: .milliseconds(3400)
        }
    }

    var ringsLeft: Int { reached(.live, .secondRing, .firstReport) }
    var ridersWaving: Int { reached(.firstWave, .secondWave, .thirdWave) }
    var reportsSent: Int { reached(.firstReport, .secondReport, .thirdReport) }
}

/// Screen 2: the three choices fill in, riders see the vehicle, and the trip
/// ends with End trip.
nonisolated enum ChoicesBeat: Int, WelcomeBeat {
    case start
    /// The three cards rise in, empty. One frame in, so the frame before
    /// them is drawn first and they have somewhere to rise from.
    case cardsRise
    case vehicleChosen
    case routeChosen
    case runChosen
    /// The green card rises in with the first report.
    case ridersSeeYou
    case secondReport
    case thirdReport
    /// The End trip row rises in.
    case rest

    var at: Duration {
        switch self {
        case .start: .zero
        case .cardsRise: .milliseconds(50)
        case .vehicleChosen: .milliseconds(600)
        case .routeChosen: .milliseconds(1100)
        case .runChosen: .milliseconds(1600)
        case .ridersSeeYou: .milliseconds(2100)
        case .secondReport: .milliseconds(2800)
        case .thirdReport: .milliseconds(3150)
        case .rest: .milliseconds(3400)
        }
    }

    var choicesMade: Int { reached(.vehicleChosen, .routeChosen, .runChosen) }
    var reportsSent: Int { reached(.ridersSeeYou, .secondReport, .thirdReport) }
}

/// Screen 3: the three things a trip needs come in one after another, each
/// icon makes its one small move, and then the card for a stopped trip.
nonisolated enum ReportingBeat: Int, WelcomeBeat {
    case start
    case locationRow
    /// The second row, as the first row's icon moves.
    case chargeRow
    /// The third row, as the second row's icon moves.
    case appRow
    /// The third row's icon moves.
    case appIcon
    case label
    /// The card for a stopped trip rises in.
    case rest

    var at: Duration {
        switch self {
        case .start: .zero
        case .locationRow: .milliseconds(150)
        case .chargeRow: .milliseconds(400)
        case .appRow: .milliseconds(650)
        case .appIcon: .milliseconds(900)
        case .label: .milliseconds(1000)
        case .rest: .milliseconds(1100)
        }
    }

    var rowsIn: Int { reached(.locationRow, .chargeRow, .appRow) }
    var iconsMoved: Int { reached(.chargeRow, .appRow, .appIcon) }
}
