import Foundation

/// Every word the Tracking screen and CarPlay's Tracking templates show, in
/// Android's words (`res/values/strings.xml`, `tracking_*`) with "vehicle" for
/// "bus". One place for all of them, so the phone and the car cannot differ.
enum TripStatusText {
    // MARK: The status card

    static var waitingForGPS: String {
        String(localized: "Waiting for GPS", comment: "Status card answer before the trip's first position fix.")
    }

    static var onTime: String {
        String(localized: "On time", comment: "Status card answer while the vehicle is inside the on-time window.")
    }

    static func minutesLate(_ minutes: Int) -> String {
        String(localized: "\(minutes) min late", comment: "Status card answer: whole minutes behind the schedule.")
    }

    static func minutesEarly(_ minutes: Int) -> String {
        String(localized: "\(minutes) min early", comment: "Status card answer: whole minutes ahead of the schedule.")
    }

    /// "On time" within a minute of the schedule, otherwise whole minutes late
    /// or early, rounded to the nearest minute: Android's `deviationText`.
    static func deviation(_ seconds: TimeInterval) -> String {
        let minutes = deviationMinutes(seconds)
        if minutes > 0 { return minutesLate(minutes) }
        if minutes < 0 { return minutesEarly(-minutes) }
        return onTime
    }

    /// Whole minutes off the schedule, positive behind it, and 0 within a
    /// minute either way.
    static func deviationMinutes(_ seconds: TimeInterval) -> Int {
        guard abs(seconds) >= 60 else { return 0 }
        let minutes = Int((abs(seconds) / 60).rounded())
        return seconds > 0 ? minutes : -minutes
    }

    static func offRoute(distance: String) -> String {
        String(localized: "Off route · \(distance)", comment: "Status card answer: how far the vehicle is from the route, e.g. 'Off route · 120 m'.")
    }

    /// The off-route answer as VoiceOver reads it: the distance changes with
    /// every fix, so it is left out (as Android).
    static var offRouteShort: String {
        String(localized: "Off route", comment: "The off-route answer without its distance, for VoiceOver and the stop list's vehicle row.")
    }

    static var offSchedule: String {
        String(localized: "Off schedule", comment: "Status card line 1 beyond the server's schedule window. The answer above it says how far off, e.g. '20 min late'.")
    }

    static var rightRun: String {
        String(localized: "Is this the right run?", comment: "Status card line 1 after 'Off schedule': the driver most likely picked the wrong run.")
    }

    static var next: String {
        String(localized: "Next", comment: "Status card line 1, before the next stop's name.")
    }

    static var from: String {
        String(localized: "From", comment: "Status card line 1 while waiting for GPS, before the trip's first stop.")
    }

    /// Two facts on one line: "7:18 AM · 232 m", or "6:58 AM · 4317".
    static func pair(_ first: String, _ second: String) -> String {
        String(localized: "\(first) · \(second)", comment: "Two facts on one line: a stop's scheduled time then the distance to it, or the run's start time then the vehicle.")
    }

    static func offScheduleNext(name: String, time: String) -> String {
        String(localized: "Next \(name) · \(time)", comment: "Status card line 2 while off schedule: the next stop's name, then its scheduled time in the agency's zone.")
    }

    static func runVehicle(time: String, vehicle: String) -> String {
        String(localized: "\(time) run · \(vehicle)", comment: "Status card line 2 while waiting for GPS: the run's start time in the agency's zone, then the vehicle, e.g. '6:58 AM run · 4317'.")
    }

    // MARK: The card for VoiceOver

    /// The card in one sentence: "On time. Next stop 3rd Ave and Seneca St,
    /// 7:18 AM, 232 metres". With no distance when none should be read.
    static func spokenNextStop(_ answer: String, name: String, time: String, distance: String?) -> String {
        if let distance {
            return String(localized: "\(answer). Next stop \(name), \(time), \(distance)", comment: "VoiceOver: the status card in one sentence. The answer, then the next stop's name, its scheduled time and the distance to it.")
        }
        return String(localized: "\(answer). Next stop \(name), \(time)", comment: "VoiceOver: the status card in one sentence, with no distance. The answer, then the next stop's name and its scheduled time.")
    }

    static func spokenOffSchedule(_ answer: String, name: String, time: String) -> String {
        String(localized: "\(answer). Off schedule, is this the right run? Next stop \(name), \(time)", comment: "VoiceOver: the status card in one sentence while off schedule. The answer, e.g. '20 min late', then the next stop's name and its scheduled time.")
    }

    static func spokenWaiting(name: String, time: String, vehicle: String) -> String {
        String(localized: "Waiting for GPS. From \(name), \(time) run, vehicle \(vehicle)", comment: "VoiceOver: the status card in one sentence before the first fix. The trip's first stop, the run's start time and the vehicle.")
    }

    /// A distance in words, for VoiceOver: whole metres below a kilometre,
    /// then kilometres to one decimal place, rounded as the card's "1.3 km".
    static func spokenDistance(_ metres: Double) -> String {
        guard metres >= Formatters.metresPerKilometre else {
            let whole = Int(metres.rounded())
            return String(localized: "\(whole) metres", comment: "VoiceOver: a distance in whole metres.")
        }
        return String(localized: "\(Formatters.kilometres(metres), specifier: "%.1f") kilometres", comment: "VoiceOver: a distance in kilometres, to one decimal place.")
    }

    // MARK: Problems under the card

    static var locationStopped: String {
        String(localized: "Location stopped", comment: "Problem line: the location stream ended during the trip.")
    }

    static var tapResume: String {
        String(localized: "Tap Resume", comment: "Problem line, after 'Location stopped': what to do. The Resume button is on the same line.")
    }

    static var resume: String {
        String(localized: "Resume", comment: "The button on the 'Location stopped' problem line that restarts location.")
    }

    static var openAppOnIPhone: String {
        String(localized: "Open the app on iPhone", comment: "Problem line: iOS refused a background location session because the app was not in the foreground.")
    }

    static var reportsStartWhenYouDo: String {
        String(localized: "Reports start when you do", comment: "Problem line, after 'Open the app on iPhone': opening the app is what restarts reporting.")
    }

    static var locationOff: String {
        String(localized: "Location off", comment: "Problem line: location access was turned off for the app during the trip.")
    }

    static var preciseLocationOff: String {
        String(localized: "Precise Location off", comment: "Problem line: Precise Location was turned off during the trip, so every fix is too coarse to report.")
    }

    static var tapToFix: String {
        String(localized: "Tap to fix", comment: "Problem line, after 'Location off' or 'Precise Location off': tapping the line opens the app's page in Settings.")
    }

    static var noGPSSignal: String {
        String(localized: "No GPS signal", comment: "Problem line: the phone has no position fix.")
    }

    static var positionPaused: String {
        String(localized: "Position paused", comment: "Problem line, after 'No GPS signal': riders see the last position until a fix returns.")
    }

    static var reportsRefused: String {
        String(localized: "Reports refused", comment: "Problem line: the server refuses the vehicle's reports (not assigned, deactivated, or no active trip).")
    }

    static var askYourDispatcher: String {
        String(localized: "Ask your dispatcher", comment: "Problem line, after 'Reports refused': only dispatch can fix the assignment.")
    }

    static var serverError: String {
        String(localized: "Server error", comment: "Problem line: the server fails or refuses the reports for a reason the driver cannot fix (a server error), so they are not in the feed.")
    }

    static var phoneClockWrong: String {
        String(localized: "Phone clock is wrong", comment: "Problem line: the server rejects the reports' timestamps.")
    }

    static var setAutomaticTime: String {
        String(localized: "Set automatic time", comment: "Problem line, after 'Phone clock is wrong': turn on automatic date and time in Settings.")
    }

    static var noConnection: String {
        String(localized: "No connection", comment: "Problem line: the app cannot reach the server.")
    }

    static var ridersCantSeeYou: String {
        String(localized: "Riders can't see you", comment: "Problem line, after 'No connection': the vehicle has dropped off riders' maps.")
    }

    /// The problem line as VoiceOver reads it: "No connection, Riders can't see you".
    static func spokenProblem(what: String, meaning: String) -> String {
        String(localized: "\(what), \(meaning)", comment: "VoiceOver: the problem line in one sentence, what happened then what it means.")
    }

    static var opensSettings: String {
        String(localized: "Opens Settings", comment: "VoiceOver hint on a problem line that opens the app's page in Settings.")
    }

    // MARK: Signed out

    static var signedOut: String {
        String(localized: "Signed out", comment: "The card that takes the status card's place once the driver's sign-in has expired.")
    }

    static var signedOutMessage: String {
        String(localized: "Reports have stopped. Your trip is still open.", comment: "Under 'Signed out': what the expired sign-in means for the trip.")
    }

    static var spokenSignedOut: String {
        String(localized: "Signed out. \(signedOutMessage)", comment: "VoiceOver: the Signed out card in one announcement, when it takes the status card's place.")
    }

    static var signIn: String {
        String(localized: "Sign in", comment: "The button on the Signed out card that opens the sign-in sheet.")
    }

    // MARK: The trip sheet

    static func sent(_ count: Int) -> String {
        String(localized: "\(count) sent", comment: "Trip bar: how many reports have reached the server, e.g. '142 sent'.")
    }

    /// The trip bar as VoiceOver reads it: "Route 7 to Downtown Seattle, 142 reports sent".
    static func spokenTripBar(route: String, headsign: String, sent: Int) -> String {
        if route.isEmpty {
            return String(localized: "\(headsign), \(sent) reports sent", comment: "VoiceOver: the trip bar in one sentence for a route with no short name: the headsign, then how many reports have reached the server.")
        }
        return String(localized: "Route \(route) to \(headsign), \(sent) reports sent", comment: "VoiceOver: the trip bar in one sentence: the route's short name, the headsign, then how many reports have reached the server.")
    }

    static var endTrip: String {
        String(localized: "End trip", comment: "The button on the trip bar that ends the trip, and the confirming button in the 'End this trip?' alert.")
    }

    static var ending: String {
        String(localized: "Ending…", comment: "On the End trip button while the end is being sent to the server.")
    }

    static var run: String {
        String(localized: "Run", comment: "Trip sheet row label: the run's start time and the vehicle follow it.")
    }

    static var reports: String {
        String(localized: "Reports", comment: "Trip sheet row label: whether reports are reaching the server follows it.")
    }

    static var reporting: String {
        String(localized: "Reporting", comment: "Trip sheet Reports row: reports are reaching the server.")
    }

    static var notReporting: String {
        String(localized: "Not reporting", comment: "Trip sheet Reports row: reports are not reaching the server (no connection, refused, or a wrong clock).")
    }

    static var noGPS: String {
        String(localized: "No GPS", comment: "Trip sheet Reports row: there is no position to report.")
    }

    static var reportsStopped: String {
        String(localized: "Reports stopped", comment: "Trip sheet Reports row: the sign-in expired, so nothing is sent until the driver signs in again.")
    }

    static func stopsPassed(_ count: Int) -> String {
        String(localized: "\(count) stops passed", comment: "Trip sheet stop list: the folded row standing in for the stops the vehicle has already passed, e.g. '6 stops passed'.")
    }

    static var expanded: String {
        String(localized: "expanded", comment: "VoiceOver value of the folded stops row while the stops it stands for are showing.")
    }

    static var collapsed: String {
        String(localized: "collapsed", comment: "VoiceOver value of the folded stops row while the stops it stands for are hidden.")
    }

    /// A stop row in one sentence: "3rd Ave and Seneca St, next stop, 7:18 AM".
    static func spokenStopRow(name: String, time: String, kind: StopRows.Kind) -> String {
        switch kind {
        case .next:
            String(localized: "\(name), next stop, \(time)", comment: "VoiceOver: a stop list row for the next stop, with its scheduled time.")
        case .last:
            String(localized: "\(name), last stop, \(time)", comment: "VoiceOver: a stop list row for the end of the line, with its scheduled time.")
        case .passed:
            String(localized: "\(name), passed, \(time)", comment: "VoiceOver: a stop list row for a stop the vehicle has passed, with its scheduled time.")
        case .upcoming:
            String(localized: "\(name), \(time)", comment: "VoiceOver: a stop list row for a stop still ahead, with its scheduled time.")
        }
    }

    /// The next stop's mark on the map: "Next stop, 3rd Ave and Seneca St".
    static func spokenNextStopMark(name: String) -> String {
        String(localized: "Next stop, \(name)", comment: "VoiceOver: the next stop's mark on the map.")
    }

    /// The vehicle's row in one sentence: "Vehicle 4317, 3 min late".
    static func spokenVehicleRow(name: String, status: String?) -> String {
        guard let status else {
            return String(localized: "Vehicle \(name)", comment: "VoiceOver: the vehicle's row of the stop list before the first fix, naming the vehicle.")
        }
        return String(localized: "Vehicle \(name), \(status)", comment: "VoiceOver: the vehicle's row of the stop list, naming the vehicle and how it is keeping to the schedule.")
    }

    static var recenter: String {
        String(localized: "Re-center", comment: "The button over the map, once the driver has dragged it away from the vehicle, that makes the map follow the vehicle again.")
    }

    static var recenterAccessibilityLabel: String {
        String(localized: "Re-center the map on the vehicle", comment: "VoiceOver label of the Re-center button over the map.")
    }

    // MARK: Ending the trip

    static var endTripTitle: String {
        String(localized: "End this trip?", comment: "Title of the alert that confirms ending the trip.")
    }

    static func endTripMessage(route: String, time: String, count: Int) -> String {
        String(localized: "Reports stop, and route \(route)'s \(time) run is marked complete. \(count) reports were sent.", comment: "Message of the 'End this trip?' alert: the route's short name, the run's start time in the agency's zone, then how many reports reached the server.")
    }

    /// The same, for a trip whose route has no short name.
    static func endTripMessage(count: Int) -> String {
        String(localized: "Reports stop, and this run is marked complete. \(count) reports were sent.", comment: "Message of the 'End this trip?' alert for a route with no short name: how many reports reached the server.")
    }

    static var keepGoing: String {
        String(localized: "Keep going", comment: "The cancel button of the 'End this trip?' and 'Trip not ended yet' alerts: the trip carries on.")
    }

    static var notEndedTitle: String {
        String(localized: "Trip not ended yet", comment: "Title of the alert shown when the server did not confirm the end of the trip.")
    }

    static var notEndedMessage: String {
        String(localized: "The server didn't confirm the end. Check your signal and try again, or stop reporting on this phone now.", comment: "Message of the 'Trip not ended yet' alert.")
    }

    static var tryAgain: String {
        String(localized: "Try again", comment: "Button in the 'Trip not ended yet' alert, and on the Resume screen after a failed end: send the end again.")
    }

    static var endOnThisPhone: String {
        String(localized: "End on this phone", comment: "Button in the 'Trip not ended yet' alert, and on the Resume screen after a failed end: stop reporting now; the end is sent before the driver's next start.")
    }
}
