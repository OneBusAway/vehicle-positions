import Foundation

/// Every word Resume shows, in Android's words (`res/values/strings.xml`,
/// `resume_*`) with "vehicle" for "bus". Try again and End on this phone are
/// Tracking's own (`TripStatusText`), as Android shares them too.
nonisolated enum ResumeText {
    static var title: String {
        String(localized: "Resume your trip?", comment: "Title of the screen at launch when the app closed while a trip was running.")
    }

    static var message: String {
        String(localized: "The app closed while this trip was running. Reports are paused until you choose.", comment: "Under the Resume screen's title.")
    }

    static func route(_ route: String) -> String {
        String(localized: "Route \(route)", comment: "Resume card: in place of the headsign, for a trip that has none. The route's short name or id, e.g. '7'.")
    }

    static func run(_ time: String) -> String {
        String(localized: "\(time) run", comment: "Resume card: the run's first departure in the agency's zone, e.g. '6:58 AM run'.")
    }

    static func vehicle(_ id: String) -> String {
        String(localized: "Vehicle \(id)", comment: "Resume card: the vehicle's fleet number, e.g. 'Vehicle 4317'.")
    }

    static func started(minutes: Int, at time: String) -> String {
        String(localized: "Started \(minutes) min ago, at \(time)", comment: "Resume card: whole minutes since the trip began, then the time it began, e.g. 'Started 23 min ago, at 6:58 AM'.")
    }

    static func started(hours: Int, minutes: Int, at time: String) -> String {
        String(localized: "Started \(hours) h \(minutes) min ago, at \(time)", comment: "Resume card: hours, then minutes past the hour, then the time the trip began, e.g. 'Started 2 h 5 min ago, at 6:58 AM'.")
    }

    static var resumeTrip: String {
        String(localized: "Resume trip", comment: "The Resume screen's main button: keep reporting on the open trip.")
    }

    static var endTrip: String {
        String(localized: "End trip", comment: "The Resume screen's second button: end the open trip.")
    }

    static var ending: String {
        String(localized: "Ending trip", comment: "The Resume screen's End trip button while the end is being sent to the server.")
    }

    static var endNotConfirmed: String {
        String(localized: "The server didn't confirm the trip ended. Try again, or end it on this phone only.", comment: "Banner on the Resume screen once the server did not confirm the end; the buttons then say Try again and End on this phone.")
    }

    static var dontEnd: String {
        String(localized: "Don't end the trip", comment: "Goes back from a failed end to Resume trip and End trip, for a driver who tapped End trip by mistake.")
    }

    // MARK: VoiceOver

    static func spokenRoute(_ route: String, headsign: String) -> String {
        if headsign.isEmpty {
            return Self.route(route)
        }
        return String(localized: "Route \(route) to \(headsign)", comment: "VoiceOver: the Resume card's route and headsign, e.g. 'Route 7 to Downtown Seattle'.")
    }

    static func spokenStarted(minutes: Int, at time: String) -> String {
        String(localized: "started \(minutes) minutes ago, at \(time)", comment: "VoiceOver: the Resume card's start, e.g. 'started 19 minutes ago, at 6:58 AM'.")
    }

    static func spokenStarted(hours: Int, minutes: Int, at time: String) -> String {
        String(localized: "started \(hours) hours \(minutes) minutes ago, at \(time)", comment: "VoiceOver: the Resume card's start from an hour on, e.g. 'started 2 hours 5 minutes ago, at 6:58 AM'.")
    }
}
