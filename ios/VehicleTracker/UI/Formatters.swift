import Foundation
import SwiftUI
import UIKit

/// Text the driver reads at a glance. Every string here is short on purpose.
enum Formatters {
    nonisolated static func clock(_ date: Date, timezone: String, locale: Locale = .current) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened, locale: locale)
        style.timeZone = TimeZone(identifier: timezone) ?? .current
        return date.formatted(style)
    }

    /// "20261024" as "Saturday, October 24": the agency's service day in words.
    /// The date is built and formatted in the agency's zone, never the
    /// device's, so the day never shifts by the two zones' offset. A string
    /// that is not a service date comes back with dashes, "2026-10-24", or as
    /// it came (Android's `serviceDay` and `serviceDateLabel`).
    static func serviceDay(_ serviceDate: String, timezone: String, locale: Locale = .current) -> String {
        guard serviceDate.count == 8, let year = Int(serviceDate.prefix(4)), let month = Int(serviceDate.dropFirst(4).prefix(2)),
              let day = Int(serviceDate.suffix(2)), let zone = TimeZone(identifier: timezone) else {
            return serviceDateLabel(serviceDate)
        }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        let components = DateComponents(year: year, month: month, day: day)
        guard components.isValidDate(in: calendar), let date = calendar.date(from: components) else {
            return serviceDateLabel(serviceDate)
        }
        return date.formatted(Date.FormatStyle(locale: locale, calendar: calendar, timeZone: zone).weekday(.wide).month(.wide).day())
    }

    /// "20260922" as "2026-09-22"; anything else as it came.
    static func serviceDateLabel(_ serviceDate: String) -> String {
        guard serviceDate.count == 8 else { return serviceDate }
        return "\(serviceDate.prefix(4))-\(serviceDate.dropFirst(4).prefix(2))-\(serviceDate.suffix(2))"
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
}

extension TripSession {
    /// An expired token looks like any other server error to the caller; the
    /// picker screens all want the same response, ending the session and
    /// letting `RootView` fall back to `SignInView`, instead of surfacing a raw
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
