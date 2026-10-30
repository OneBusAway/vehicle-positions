import Foundation
import SwiftUI
import UIKit

/// Text the driver reads at a glance. Every string here is short on purpose.
enum Formatters {
    /// "on time" within a minute, otherwise whole minutes late or early,
    /// rounded to the nearest minute.
    static func deviation(_ seconds: TimeInterval) -> String {
        guard abs(seconds) >= 60 else { return String(localized: "on time") }
        let minutes = Int((abs(seconds) / 60).rounded())
        return seconds > 0
            ? String(localized: "\(minutes) min late")
            : String(localized: "\(minutes) min early")
    }

    static func clock(_ date: Date, timezone: String) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened)
        style.timeZone = TimeZone(identifier: timezone) ?? .current
        return date.formatted(style)
    }

    static let metresPerKilometre = 1000.0

    static func distance(_ metres: Double) -> String {
        if metres < metresPerKilometre { return "\(Int(metres.rounded())) m" }
        return String(format: "%.1f km", kilometres(metres))
    }

    /// `metres` in kilometres to one decimal place, rounded away from zero on
    /// a tie: printf's round-half-to-even would print "1.2 km" for 1250 m.
    static func kilometres(_ metres: Double) -> Double {
        (metres / 100).rounded() / 10
    }
}

extension Adherence {
    /// OneBusAway's convention: green on time, red early, blue late.
    var tone: StatusTone {
        switch status {
        case .onTime: .onTime
        case .early: .early
        case .late: .late
        case .offSchedule: scheduleDeviation < 0 ? .early : .late
        case .offRoute: .neutral
        }
    }

    var statusLabel: String {
        switch status {
        case .onTime, .early, .late: Formatters.deviation(scheduleDeviation)
        case .offSchedule: String(localized: "Off schedule · \(Formatters.deviation(scheduleDeviation))")
        case .offRoute: String(localized: "Off route · \(Formatters.distance(projection.distanceToShape)) from the route")
        }
    }
}

extension TripSession.ReportingStatus {
    var label: String {
        switch self {
        case .connected: String(localized: "Reporting")
        case .noNetwork: String(localized: "No connection")
        case .noGPS: String(localized: "No GPS")
        case .authExpired: String(localized: "Signed out — sign in again")
        case .clockSkew: String(localized: "Check the phone's clock")
        case .refused: String(localized: "Reports refused — ask your dispatcher")
        case .needsForeground: String(localized: "Open the app on iPhone")
        case .locationLost: String(localized: "Location stopped — tap Resume")
        case .locationDenied: String(localized: "Location off · Tap to fix")
        case .preciseLocationOff: String(localized: "Precise Location off · Tap to fix")
        }
    }
}

extension TripSession {
    /// An expired token looks like any other server error to the caller; the
    /// picker screens all want the same response, ending the session and
    /// letting `RootView` fall back to `LoginView`, instead of surfacing a raw
    /// "Server error 401" alongside the form. The client has already tried a
    /// refresh, so a 401 here means the server ended the session: there is
    /// nothing left to log out of.
    @discardableResult
    func handleIfUnauthorized(_ error: any Error) -> Bool {
        if case APIError.status(401, _) = error {
            endSessionLocally()
            return true
        }
        return false
    }
}

extension URL {
    /// This app's page in the Settings app, where its location access is set.
    static var appSettings: URL {
        // A constant iOS defines; it always parses.
        URL(string: UIApplication.openSettingsURLString)!
    }
}

extension Color {
    /// A GTFS route colour: six hex digits, with or without a leading '#'.
    /// One parser, in `UIColor`, so the badge and the map cannot disagree
    /// about what a feed's colour means.
    init?(hex: String) {
        guard let ui = UIColor(hex: hex) else { return nil }
        self.init(uiColor: ui)
    }
}

/// Which run of a route the driver is most likely about to drive.
nonisolated enum TripRun {
    /// Of the runs not over yet, the one starting nearest `now`. Not the
    /// earliest still running: on a frequent route that run can be an hour in,
    /// and the driver is about to drive the one starting nearest now. A tie
    /// goes to the later start, since the earlier run is already under way.
    static func highlighted(in trips: [TripSummary], now: Date) -> TripSummary? {
        trips.filter { $0.endsAt >= now }.min { a, b in
            let fromA = abs(a.startsAt.timeIntervalSince(now)), fromB = abs(b.startsAt.timeIntervalSince(now))
            return fromA == fromB ? a.startsAt > b.startsAt : fromA < fromB
        }
    }

    /// The run the list opens at: the one just before the highlighted run, so
    /// the highlighted run is in view under it, where Android's board opens.
    /// The last run once every run is over; nil for an empty list.
    static func openAt(in trips: [TripSummary], now: Date) -> String? {
        guard let marked = highlighted(in: trips, now: now), let index = trips.firstIndex(of: marked) else {
            return trips.last?.id
        }
        return trips[max(index - 1, 0)].id
    }
}
