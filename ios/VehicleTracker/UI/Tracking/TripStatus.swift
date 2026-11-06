import Foundation

/// The trip as the driver should see it right now, judged once from the
/// session's state: the status card, the problem line or the Signed out card
/// in its place, the reports word on the trip bar, the ring round the vehicle
/// on the map, and what VoiceOver says. The phone screen and CarPlay both read
/// this one value, so they cannot disagree about any of it.
struct TripStatus: Equatable {
    /// The kind of answer on the card, apart from its numbers. "6 min late"
    /// and "7 min late" are the same kind: only a change of kind is animated
    /// and announced.
    enum Answer: Equatable {
        case waiting, onTime, late, early, offSchedule, offRoute
    }

    /// The status card: a symbol on the tone's fill, the answer, and two lines
    /// under it. Line 1 is a label ("Next") and what it introduces, in heavier
    /// type; line 2 is a time and a distance.
    struct Card: Equatable {
        var tone: StatusTone
        var symbol: String
        var answer: String
        var label: String
        var emphasis: String
        var line2: String
    }

    /// One reporting problem under the card: the highest-ranking of any the
    /// session has (DESIGN-iOS §4.7.2).
    struct Problem: Equatable {
        enum Kind: Equatable {
            case locationStopped, needsForeground, locationDenied, preciseLocationOff, noGPS, refused, clockSkew, noNetwork
        }

        enum Action: Equatable {
            /// A Resume button at the line's trailing end.
            case resume
            /// The whole line opens the app's page in Settings.
            case openSettings
        }

        var kind: Kind
        var symbol: String
        /// What happened, then what it means for the driver: "No connection"
        /// and "Riders can't see you".
        var what: String
        var meaning: String
        var action: Action?
    }

    /// Whether the server is hearing from the phone, as the trip bar and the
    /// sheet's Reports row say it; "Ending…" while the end is being sent.
    struct Reports: Equatable {
        enum Word: Equatable {
            case reporting, notReporting, noGPS, stopped, ending
        }

        var word: Word
        var symbol: String

        var isProblem: Bool {
            switch word {
            case .reporting, .ending: false
            case .notReporting, .noGPS, .stopped: true
            }
        }

        var text: String {
            switch word {
            case .reporting: TripStatusText.reporting
            case .notReporting: TripStatusText.notReporting
            case .noGPS: TripStatusText.noGPS
            case .stopped: TripStatusText.reportsStopped
            case .ending: TripStatusText.ending
            }
        }
    }

    /// What the card's animations and VoiceOver's announcements react to: the
    /// kind of answer, and which problem, if any, is showing. Minutes and
    /// metres moving inside one kind change nothing here.
    struct Category: Equatable {
        var answer: Answer
        var problem: Problem.Kind?
        var isSignedOut: Bool
    }

    let active: ActiveTrip
    let answer: Answer
    let card: Card
    /// Nil while all is well, and while signed out: the Signed out card takes
    /// the status card's place, so no line shows under it.
    let problem: Problem?
    let isSignedOut: Bool
    let reports: Reports
    /// The tone of the ring round the vehicle on the map: neutral before the
    /// first fix, off the route, while the position is paused and while signed
    /// out (P36), otherwise the card's.
    let ring: StatusTone
    /// The card as VoiceOver reads it, in one sentence.
    let sentence: String
    /// When the run starts, on the agency's clock: "6:58 AM". Nil for a trip
    /// with no stops.
    let runTime: String?
    /// The vehicle as the driver knows it: its label, or its id when the feed
    /// gives none.
    let vehicleName: String
    /// The stop list's vehicle row: the exact deviation, "3 min late" even
    /// inside the on-time window; "Off route" off the route; nil before the
    /// first fix, when there is no row to put it in.
    let vehicleRowStatus: String?
    /// The next stop's place in the trip's stops, by id and sequence: a loop
    /// calls at the same stop twice. Nil before the first fix.
    let nextStopIndex: Int?

    var category: Category {
        Category(answer: answer, problem: problem?.kind, isSignedOut: isSignedOut)
    }

    /// What VoiceOver announces when the category changes. The card's
    /// sentence alone would not say what went wrong, so a problem that has
    /// just appeared, or the Signed out card, is read in its place.
    func announcement(after old: Category) -> String {
        if isSignedOut {
            return old.isSignedOut ? sentence : TripStatusText.spokenSignedOut
        }
        if let problem, problem.kind != old.problem {
            return TripStatusText.spokenProblem(what: problem.what, meaning: problem.meaning)
        }
        return sentence
    }

    /// Nil when there is no trip to describe: signed out, idle, or starting.
    init?(phase: TripSession.Phase, latest: Adherence?, reporting: TripSession.ReportingStatus) {
        let active: ActiveTrip
        switch phase {
        case .active(let trip), .paused(let trip), .ending(let trip): active = trip
        case .signedOut, .idle, .starting: return nil
        }
        let vehicleName = active.vehicle.label.isEmpty ? active.vehicle.id : active.vehicle.label
        let judged = Self.judge(latest, trip: active.trip, vehicleName: vehicleName)
        let isSignedOut = reporting == .authExpired
        let problem = isSignedOut ? nil : Self.problemKind(phase: phase, reporting: reporting).map(Self.line)

        self.active = active
        self.vehicleName = vehicleName
        runTime = active.trip.stops.first.map { Formatters.clock($0.departureAt, timezone: active.trip.timezone) }
        answer = judged.answer
        card = judged.card
        sentence = judged.sentence
        vehicleRowStatus = judged.vehicleRowStatus
        nextStopIndex = latest.flatMap { judged in
            active.trip.stops.firstIndex { $0.id == judged.nextStop.id && $0.sequence == judged.nextStop.sequence }
        }
        self.isSignedOut = isSignedOut
        self.problem = problem
        reports = Self.reports(isSignedOut: isSignedOut, isEnding: phase.isEnding, problem: problem)
        ring = Self.ring(latest: latest, isSignedOut: isSignedOut, problem: problem)
    }

    // MARK: The card

    private struct Judgement {
        var answer: Answer
        var card: Card
        var sentence: String
        var vehicleRowStatus: String?
    }

    private static func judge(_ latest: Adherence?, trip: TripGeometry, vehicleName: String) -> Judgement {
        guard let latest else {
            return waiting(trip: trip, vehicleName: vehicleName)
        }
        let tone = latest.tone
        let stop = latest.nextStop
        let time = Formatters.clock(stop.arrivalAt, timezone: trip.timezone)
        let distance = Formatters.distance(latest.distanceToNextStop)
        let deviation = TripStatusText.deviation(latest.scheduleDeviation)

        /// A card that names the next stop, with its time and distance under it.
        func nextStop(_ answer: Answer, _ symbol: String, _ text: String, spokenDistance: String?, row: String) -> Judgement {
            let spoken = answer == .offRoute ? TripStatusText.offRouteShort : text
            return Judgement(
                answer: answer,
                card: Card(tone: tone, symbol: symbol, answer: text, label: TripStatusText.next, emphasis: stop.name,
                           line2: TripStatusText.pair(time, distance)),
                sentence: TripStatusText.spokenNextStop(spoken, name: stop.name, time: time, distance: spokenDistance),
                vehicleRowStatus: row
            )
        }

        let spokenDistance = TripStatusText.spokenDistance(latest.distanceToNextStop)
        switch latest.status {
        case .onTime:
            // Inside the window the card always says On time; the exact
            // minutes live on the stop list's vehicle row (P33).
            return nextStop(.onTime, "checkmark.circle.fill", TripStatusText.onTime, spokenDistance: spokenDistance, row: deviation)
        case .late:
            return nextStop(.late, "hourglass.bottomhalf.filled", deviation, spokenDistance: spokenDistance, row: deviation)
        case .early:
            return nextStop(.early, "gauge.with.dots.needle.67percent", deviation, spokenDistance: spokenDistance, row: deviation)
        case .offSchedule:
            return Judgement(
                answer: .offSchedule,
                card: Card(tone: tone, symbol: "calendar.badge.exclamationmark", answer: deviation,
                           label: TripStatusText.offSchedule, emphasis: TripStatusText.rightRun,
                           line2: TripStatusText.offScheduleNext(name: stop.name, time: time)),
                sentence: TripStatusText.spokenOffSchedule(deviation, name: stop.name, time: time),
                vehicleRowStatus: deviation
            )
        case .offRoute:
            // VoiceOver hears "Off route" and no distances: both change with
            // every fix (as Android).
            let text = TripStatusText.offRoute(distance: Formatters.distance(latest.projection.distanceToShape))
            return nextStop(.offRoute, "mappin.slash", text, spokenDistance: nil, row: TripStatusText.offRouteShort)
        }
    }

    /// Before the first fix: where the run starts, and when. A trip with no
    /// stops is refused at start and ended on resume, so its lines are empty
    /// rather than invented.
    private static func waiting(trip: TripGeometry, vehicleName: String) -> Judgement {
        let answer = TripStatusText.waitingForGPS
        guard let first = trip.stops.first else {
            return Judgement(answer: .waiting,
                             card: Card(tone: .neutral, symbol: "location.magnifyingglass", answer: answer, label: "", emphasis: "", line2: ""),
                             sentence: answer, vehicleRowStatus: nil)
        }
        let time = Formatters.clock(first.departureAt, timezone: trip.timezone)
        return Judgement(
            answer: .waiting,
            card: Card(tone: .neutral, symbol: "location.magnifyingglass", answer: answer, label: TripStatusText.from,
                       emphasis: first.name, line2: TripStatusText.runVehicle(time: time, vehicle: vehicleName)),
            sentence: TripStatusText.spokenWaiting(name: first.name, time: time, vehicle: vehicleName),
            vehicleRowStatus: nil
        )
    }

    // MARK: Problems

    /// The first match in DESIGN-iOS §4.7.2's order. Signed out is judged
    /// before this is called, so `.authExpired` cannot reach it; a paused trip
    /// reports nothing, whatever `reporting` was left saying.
    private static func problemKind(phase: TripSession.Phase, reporting: TripSession.ReportingStatus) -> Problem.Kind? {
        if case .paused = phase { return .locationStopped }
        switch reporting {
        case .connected, .authExpired: return nil
        case .locationLost: return .locationStopped
        case .needsForeground: return .needsForeground
        case .locationDenied: return .locationDenied
        case .preciseLocationOff: return .preciseLocationOff
        case .noGPS: return .noGPS
        case .refused: return .refused
        case .clockSkew: return .clockSkew
        case .noNetwork: return .noNetwork
        }
    }

    private static func line(for kind: Problem.Kind) -> Problem {
        switch kind {
        case .locationStopped:
            Problem(kind: kind, symbol: "location.slash.fill", what: TripStatusText.locationStopped, meaning: TripStatusText.tapResume, action: .resume)
        case .needsForeground:
            Problem(kind: kind, symbol: "iphone", what: TripStatusText.openAppOnIPhone, meaning: TripStatusText.reportsStartWhenYouDo, action: nil)
        case .locationDenied:
            Problem(kind: kind, symbol: "location.slash.fill", what: TripStatusText.locationOff, meaning: TripStatusText.tapToFix, action: .openSettings)
        case .preciseLocationOff:
            Problem(kind: kind, symbol: "scope", what: TripStatusText.preciseLocationOff, meaning: TripStatusText.tapToFix, action: .openSettings)
        case .noGPS:
            Problem(kind: kind, symbol: "location.slash.fill", what: TripStatusText.noGPSSignal, meaning: TripStatusText.positionPaused, action: nil)
        case .refused:
            Problem(kind: kind, symbol: "hand.raised.fill", what: TripStatusText.reportsRefused, meaning: TripStatusText.askYourDispatcher, action: nil)
        case .clockSkew:
            Problem(kind: kind, symbol: "clock.badge.exclamationmark", what: TripStatusText.phoneClockWrong, meaning: TripStatusText.setAutomaticTime, action: nil)
        case .noNetwork:
            Problem(kind: kind, symbol: "icloud.slash.fill", what: TripStatusText.noConnection, meaning: TripStatusText.ridersCantSeeYou, action: nil)
        }
    }

    /// Whether the problem is that there is no position to report, as against
    /// a position the server is not getting.
    private static func pausesThePosition(_ kind: Problem.Kind) -> Bool {
        switch kind {
        case .locationStopped, .locationDenied, .preciseLocationOff, .noGPS: true
        case .needsForeground, .refused, .clockSkew, .noNetwork: false
        }
    }

    /// Signed out outranks everything. Then, while the end is being sent,
    /// "Ending…" whatever the trip was doing before: a paused trip's reports
    /// had already stopped, so Reporting would be the wrong word.
    private static func reports(isSignedOut: Bool, isEnding: Bool, problem: Problem?) -> Reports {
        if isSignedOut { return Reports(word: .stopped, symbol: "lock.fill") }
        if isEnding { return Reports(word: .ending, symbol: "hourglass") }
        guard let problem else { return Reports(word: .reporting, symbol: "checkmark.icloud.fill") }
        return Reports(word: pausesThePosition(problem.kind) ? .noGPS : .notReporting, symbol: problem.symbol)
    }

    private static func ring(latest: Adherence?, isSignedOut: Bool, problem: Problem?) -> StatusTone {
        guard let latest, !isSignedOut else { return .neutral }
        if let problem, pausesThePosition(problem.kind) { return .neutral }
        return latest.tone
    }
}
