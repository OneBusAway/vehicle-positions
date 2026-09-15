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

    static func distance(_ metres: Double) -> String {
        if metres < 1000 { return "\(Int(metres.rounded())) m" }
        // Round to one decimal place ourselves (away from zero on a tie):
        // printf's round-half-to-even would print "1.2 km" for 1250 m.
        let km = (metres / 100).rounded() / 10
        return String(format: "%.1f km", km)
    }

    static func elapsed(_ seconds: TimeInterval) -> String {
        let total = Int(seconds)
        let h = total / 3600, m = (total % 3600) / 60, s = total % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, s) : String(format: "%d:%02d", m, s)
    }
}

extension Adherence {
    /// OneBusAway's convention: green on time, red early, blue late.
    var color: Color {
        switch status {
        case .onTime: .green
        case .early: .red
        case .late: .blue
        case .offSchedule: scheduleDeviation < 0 ? .red : .blue
        case .offRoute: .gray
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
        case .needsForeground: String(localized: "Open the app on iPhone")
        case .locationLost: String(localized: "Location stopped — tap Resume")
        }
    }

    var isProblem: Bool {
        if case .connected = self { return false }
        return true
    }
}

/// The banner across the top of the tracking screen. It reads the phase as
/// well as the reporting status, because a trip a relaunch found is not
/// reporting anything yet — and `reporting` starts out `.connected`, which
/// would otherwise paint a paused trip green.
enum TrackingBanner {
    static func text(phase: TripSession.Phase, reporting: TripSession.ReportingStatus) -> String {
        switch phase {
        case .paused: String(localized: "Paused — not reporting")
        case .ending: String(localized: "Ending…")
        default: reporting.label
        }
    }

    static func color(phase: TripSession.Phase, reporting: TripSession.ReportingStatus) -> Color {
        switch phase {
        case .paused: .orange
        case .ending: .gray
        default: reporting.isProblem ? .red : .green
        }
    }
}

extension TripSession {
    /// An expired token looks like any other server error to the caller; the
    /// picker screens all want the same response — sign out and let
    /// `RootView` fall back to `LoginView` — instead of surfacing a raw
    /// "Server error 401" alongside the form.
    @discardableResult
    func handleIfUnauthorized(_ error: any Error) -> Bool {
        if case APIError.status(401, _) = error {
            signOut()
            return true
        }
        return false
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
    static func highlighted(in trips: [TripSummary], now: Date) -> TripSummary? {
        if let current = trips.first(where: { $0.startsAt <= now && now <= $0.endsAt }) { return current }
        return trips.filter { $0.startsAt > now }.min { $0.startsAt < $1.startsAt }
    }
}
