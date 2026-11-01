import Foundation

/// The open trip as Resume shows it (DESIGN-iOS §4.8, Android's
/// `ResumeCard` and `StartedAgo`): the route's badge, where the run goes and
/// when it began, the vehicle, and how long ago the trip started. Times are
/// the trip's own zone, the agency's, whatever the phone's is.
nonisolated struct ResumeCard: Equatable {
    let badgeName: String
    let routeColor: String
    let routeTextColor: String
    /// The headsign, or the route by name when the trip has none.
    let title: String
    /// The run's first departure, e.g. "6:58 AM"; nil for a trip with no stops.
    let runTime: String?
    let vehicleID: String
    let startedAt: Date
    /// When the trip started, e.g. "6:58 AM".
    let startTime: String
    private let headsign: String

    init(_ active: ActiveTrip, locale: Locale = .current) {
        let trip = active.trip
        badgeName = trip.badgeName
        routeColor = trip.route.color
        routeTextColor = trip.route.textColor
        headsign = trip.headsign
        title = trip.headsign.isEmpty ? ResumeText.route(trip.badgeName) : trip.headsign
        runTime = trip.stops.first.map { Formatters.clock($0.departureAt, timezone: trip.timezone, locale: locale) }
        vehicleID = active.vehicle.id
        startedAt = active.startedAt
        startTime = Formatters.clock(active.startedAt, timezone: trip.timezone, locale: locale)
    }

    /// "Started 19 min ago, at 6:58 AM", or "Started 2 h 5 min ago, at 6:58
    /// AM" from an hour on, with the minutes shown even at 0. Whole minutes,
    /// rounded down, and never below 0, for a phone clock behind the start.
    func started(now: Date) -> String {
        let (hours, minutes) = elapsed(now: now)
        guard let hours else { return ResumeText.started(minutes: minutes, at: startTime) }
        return ResumeText.started(hours: hours, minutes: minutes, at: startTime)
    }

    /// The route and where it goes, as VoiceOver reads the title: "Route 7
    /// to Downtown Seattle".
    var spokenRoute: String {
        ResumeText.spokenRoute(badgeName, headsign: headsign)
    }

    /// `started(now:)` in words for VoiceOver: "started 19 minutes ago, at
    /// 6:58 AM".
    func spokenStarted(now: Date) -> String {
        let (hours, minutes) = elapsed(now: now)
        guard let hours else { return ResumeText.spokenStarted(minutes: minutes, at: startTime) }
        return ResumeText.spokenStarted(hours: hours, minutes: minutes, at: startTime)
    }

    /// Whole hours from 60 minutes on, and the minutes past them.
    private func elapsed(now: Date) -> (hours: Int?, minutes: Int) {
        let minutes = Int(max(0, now.timeIntervalSince(startedAt)) / 60)
        return minutes < 60 ? (nil, minutes) : (minutes / 60, minutes % 60)
    }
}
