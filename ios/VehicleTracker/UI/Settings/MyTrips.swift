import Foundation

/// My trips as it loads, a page at a time (the design's sheet 10): the trips
/// the driver has ended, newest first. The screen keeps one in `@State`, and
/// every change goes through here.
nonisolated struct MyTripsList: Equatable {
    /// The server's own default page.
    static let pageSize = 50

    nonisolated enum Load: Equatable {
        case loading, failed, loaded
    }

    /// The next page to ask for. `generation` tells a page asked for before a
    /// refresh from one asked for after it.
    nonisolated struct PageRequest: Hashable {
        let offset: Int
        let generation: Int
    }

    private(set) var load: Load = .loading
    /// Ended trips, newest first. A running one is left out, as the empty
    /// page says: "A trip shows here after you end it."
    private(set) var trips: [MyTrip] = []
    private(set) var hasMore = false
    /// A pull to refresh failed: the list stays, with a banner saying so.
    private(set) var refreshFailed = false
    /// The next page failed: the end of the list says so, with Try again.
    private(set) var moreFailed = false
    /// How many trips the server has sent, a running one included: where the
    /// next page starts.
    private var received = 0
    private var generation = 0

    /// Try again, after the first page failed: the spinner again.
    mutating func reload() {
        load = .loading
    }

    /// The first page, from the first load, Try again or a pull to refresh. It
    /// replaces whatever was there.
    mutating func receivedFirst(_ page: MyTripsPage) {
        generation += 1
        trips = page.trips.filter { $0.endTime != nil }
        received = page.trips.count
        hasMore = page.hasMore
        refreshFailed = false
        moreFailed = false
        load = .loaded
    }

    /// A refresh that fails keeps the list; a first load takes the screen.
    mutating func firstFailed() {
        if load == .loaded {
            refreshFailed = true
        } else {
            load = .failed
        }
    }

    /// The page to load as the list ends, if there is one to load now.
    var nextPage: PageRequest? {
        guard load == .loaded, hasMore, !moreFailed else { return nil }
        return PageRequest(offset: received, generation: generation)
    }

    /// A trip started since the first page pushes the rest one place down, so
    /// the next page can repeat the last trip already here; it shows once.
    mutating func received(_ page: MyTripsPage, for request: PageRequest) {
        guard request == nextPage else { return }
        let shown = Set(trips.map(\.id))
        trips += page.trips.filter { $0.endTime != nil && !shown.contains($0.id) }
        received += page.trips.count
        hasMore = page.hasMore
    }

    mutating func failed(_ request: PageRequest) {
        guard request == nextPage else { return }
        moreFailed = true
    }

    /// Try again at the end of the list.
    mutating func retryMore() {
        moreFailed = false
    }
}

/// The trips that started on one day, under its title: Today, Yesterday, or
/// the date, in the phone's time zone.
nonisolated struct MyTripsDay: Equatable, Identifiable {
    /// The day's start.
    let id: Date
    let title: String
    let trips: [MyTrip]

    /// `trips`, newest first, under their days. The year shows only for a
    /// day in another year.
    static func of(_ trips: [MyTrip], now: Date, calendar: Calendar = .current, locale: Locale = .current) -> [MyTripsDay] {
        var days: [MyTripsDay] = []
        for trip in trips {
            let day = calendar.startOfDay(for: trip.startTime)
            if let last = days.last, last.id == day {
                days[days.count - 1] = MyTripsDay(id: day, title: last.title, trips: last.trips + [trip])
            } else {
                days.append(MyTripsDay(id: day, title: title(of: day, now: now, calendar: calendar, locale: locale), trips: [trip]))
            }
        }
        return days
    }

    private static func title(of day: Date, now: Date, calendar: Calendar, locale: Locale) -> String {
        if calendar.isDate(day, inSameDayAs: now) { return MyTripsText.today }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(day, inSameDayAs: yesterday) {
            return MyTripsText.yesterday
        }
        let style = Date.FormatStyle(locale: locale, calendar: calendar, timeZone: calendar.timeZone).weekday(.wide).month(.wide).day()
        let sameYear = calendar.isDate(day, equalTo: now, toGranularity: .year)
        return day.formatted(sameYear ? style : style.year())
    }
}

/// What a row of My trips shows: the route's badge, where the trip went, when
/// it ran and on which vehicle. Nothing opens from it.
nonisolated struct MyTripRow: Equatable {
    /// The route's short name, or its id when the schedule no longer has it.
    let badge: String
    let color: String
    let textColor: String
    /// The headsign, or the route's name when the trip has none.
    let title: String?
    let detail: String

    init(_ trip: MyTrip, calendar: Calendar = .current, locale: Locale = .current) {
        let route = trip.route
        badge = route.map { $0.shortName.isEmpty ? trip.routeID : $0.shortName } ?? trip.routeID
        color = route?.color ?? ""
        textColor = route?.textColor ?? ""
        title = [trip.headsign, route?.longName ?? ""].first { !$0.isEmpty }
        let time = Date.FormatStyle(date: .omitted, time: .shortened, locale: locale, calendar: calendar, timeZone: calendar.timeZone)
        let end = trip.endTime.map { $0.formatted(time) } ?? ""
        detail = MyTripsText.times(trip.startTime.formatted(time), end, vehicle: trip.vehicleID)
    }

    /// The row as VoiceOver reads it: the route, where to, then the rest.
    var spoken: String {
        [badge.isEmpty ? nil : PickerText.route(badge), title, detail].compactMap(\.self).joined(separator: ", ")
    }
}
