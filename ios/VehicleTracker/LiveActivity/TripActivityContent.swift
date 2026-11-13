import Foundation

extension TripActivityAttributes {
    /// The trip's fixed facts, read once when its activity starts. The stale
    /// words are Android's notification for reports that have stopped.
    init(_ active: ActiveTrip) {
        self.init(serverTripID: active.serverTripID, routeName: active.trip.badgeName,
                  routeColor: active.trip.route.color, routeTextColor: active.trip.route.textColor,
                  headsign: active.trip.headsign,
                  staleLine: TripStatusText.pair(TripStatusText.reportsPaused, TripStatusText.openTheAppToKeepReporting),
                  staleTitle: TripStatusText.reportsPaused)
    }
}

extension TripActivityAttributes.ContentState {
    /// `status` in the Live Activity's words, in this order: signed out, a
    /// problem, then the answer; while the end is being sent the reports line
    /// says so whatever came before. The next-stop line is built from
    /// `latest`, never from the card's lines: before the first fix those name
    /// the first stop, and off schedule they ask whether this is the right run.
    init(status: TripStatus, latest: Adherence?) {
        let trip = status.active.trip
        let speak = { TripStatusText.spokenLiveActivity(route: trip.route.shortName, headsign: trip.headsign, status: $0) }
        tone = status.ring

        if status.isSignedOut {
            attention = true
            symbol = "lock.fill"
            lead = TripStatusText.signedOut
            minutes = nil
            next = nil
            reportsSymbol = nil
            reports = TripStatusText.openTheAppToSignIn
            spoken = speak(TripStatusText.spokenSignedOut)
            return
        }

        // The sentence before the route and headsign go in front of it.
        var said: String
        let nextStop = latest.map { (name: $0.nextStop.name, time: Formatters.clock($0.nextStop.arrivalAt, timezone: trip.timezone)) }
        next = nextStop.map { stop in
            let line = TripStatusText.nextStopAndTime(name: stop.name, time: stop.time)
            // The card's "Off schedule" line asks about the run; here the
            // words go in front of the next stop, so they reach the Lock Screen.
            return status.answer == .offSchedule ? TripStatusText.pair(TripStatusText.offSchedule, line) : line
        }

        if let problem = status.problem {
            // A line with an action is a tap on the phone's screen (Resume,
            // or Settings), which a Live Activity can't take.
            let meaning = problem.action == nil ? problem.meaning : TripStatusText.openTheAppToKeepReporting
            attention = true
            symbol = problem.symbol
            lead = problem.what
            minutes = nil
            reportsSymbol = nil
            reports = meaning
            said = TripStatusText.spokenProblem(what: problem.what, meaning: meaning)
        } else {
            // Off route without its distance, which changes with every fix.
            let answer = status.answer == .offRoute ? TripStatusText.offRouteShort : status.card.answer
            attention = false
            symbol = status.card.symbol
            lead = answer
            switch (status.answer, latest) {
            case (.late, let latest?), (.early, let latest?):
                minutes = TripStatusText.minutesShort(abs(TripStatusText.deviationMinutes(latest.scheduleDeviation)))
            default:
                minutes = nil
            }
            reportsSymbol = status.reports.symbol
            reports = status.reports.text
            switch (status.answer, nextStop) {
            case (.offSchedule, let stop?):
                said = TripStatusText.spokenOffSchedule(answer, name: stop.name, time: stop.time)
            case (_, let stop?):
                said = TripStatusText.spokenNextStop(answer, name: stop.name, time: stop.time, distance: nil)
            case (_, nil):
                // Before the first fix: the first stop and the run, which no fix changes.
                said = status.sentence
            }
        }

        if status.reports.word == .ending {
            reportsSymbol = status.reports.symbol
            reports = status.reports.text
            // A problem's second line is what to do about it, which no longer
            // applies once the trip is ending.
            said = TripStatusText.pair(status.problem?.what ?? said, status.reports.text)
        }
        spoken = speak(said)
    }
}
