import Foundation
import SwiftUI

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

extension Color {
    /// A GTFS route colour: six hex digits, with or without a leading '#'.
    init?(hex: String) {
        var s = hex.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("#") { s.removeFirst() }
        guard s.count == 6, let v = UInt32(s, radix: 16) else { return nil }
        self.init(red: Double((v >> 16) & 0xFF) / 255, green: Double((v >> 8) & 0xFF) / 255, blue: Double(v & 0xFF) / 255)
    }
}

/// Which run of a route the driver is most likely about to drive.
nonisolated enum TripRun {
    static func highlighted(in trips: [TripSummary], now: Date) -> TripSummary? {
        if let current = trips.first(where: { $0.startsAt <= now && now <= $0.endsAt }) { return current }
        return trips.filter { $0.startsAt > now }.min { $0.startsAt < $1.startsAt }
    }
}
