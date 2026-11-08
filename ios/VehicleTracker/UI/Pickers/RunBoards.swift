import Foundation

/// Whether the highlighted run is the one under way or the next one to start.
nonisolated enum RunHighlight: Equatable {
    case now, next
}

/// One run as the departure board draws it.
nonisolated struct BoardRun: Equatable, Identifiable {
    let id: String
    let startsAt: Date
    let headsign: String
    let label: RunLabel
    /// Gone: every run before the highlighted one, or all of them once the
    /// day's runs are over.
    let past: Bool
    let highlight: RunHighlight?
}

/// One direction of a route's runs as the board's switch offers it:
/// `headsign` is the one most of its runs show, for the switch's "To" label,
/// and `firstStop` and `lastStop` are the ends most of them run between.
nonisolated struct RunDirection: Equatable {
    let id: Int
    let headsign: String
    let firstStop: String
    let lastStop: String
}

/// One direction's departures, or all of a route's when the runs do not
/// split into two directions; `direction` is nil for that single board,
/// which has no switch over it.
nonisolated struct Board: Equatable {
    let direction: RunDirection?
    let runs: [BoardRun]
    /// Where the Now line goes: before the run at this index, after the last
    /// once every run has gone; nil while no run has gone yet.
    let nowAt: Int?
    /// The run the board opens at: the run just gone, so the Now line and
    /// the run under way or next sit under it; the last run once all are
    /// over; nil for an empty board.
    let openAt: String?
}

/// A route's boards at one moment, and the index of the one the screen opens
/// on: a port of Android's `departureBoardsOf` and `directionsOf`.
nonisolated struct RunBoards: Equatable {
    let boards: [Board]
    let start: Int

    /// Each board has its own Now or Next run, so switching direction shows
    /// where that direction's day is. The screen opens on the direction of
    /// the route's Now or Next run, and on the first once the day is over.
    static func of(_ page: RouteTripsPage, now: Date) -> RunBoards {
        guard let directions = directions(of: page.trips) else {
            return RunBoards(boards: [board(nil, page.trips, now: now)], start: 0)
        }
        let boards = directions.map { direction in
            board(direction, page.trips.filter { $0.directionID == direction.id }, now: now)
        }
        let highlighted = TripRun.highlighted(in: page.trips, now: now)
        let start = highlighted.flatMap { run in directions.firstIndex { $0.id == run.directionID } } ?? 0
        return RunBoards(boards: boards, start: start)
    }

    private static func board(_ direction: RunDirection?, _ trips: [TripSummary], now: Date) -> Board {
        let highlighted = TripRun.highlighted(in: trips, now: now)
        // The runs come in first-departure order, so every run before the
        // highlighted one has gone.
        let gone = highlighted.flatMap { trips.firstIndex(of: $0) } ?? trips.count
        let runs = trips.enumerated().map { index, trip in
            BoardRun(id: trip.id, startsAt: trip.startsAt, headsign: trip.headsign,
                     label: RunLabel(number: trip.shortName, block: trip.blockID), past: index < gone,
                     highlight: trip == highlighted ? (trip.startsAt <= now ? .now : .next) : nil)
        }
        return Board(direction: direction, runs: runs, nowAt: gone > 0 ? gone : nil,
                     openAt: TripRun.openAt(in: trips, now: now))
    }

    /// The board's two directions, in `direction_id` order, or nil when the
    /// runs do not split into exactly two: a feed without `direction_id`, or
    /// one that gives only some runs one, keeps a single board.
    private static func directions(of trips: [TripSummary]) -> [RunDirection]? {
        var ids: [Int] = []
        for trip in trips {
            guard let id = trip.directionID else { return nil }
            if !ids.contains(id) { ids.append(id) }
        }
        guard ids.count == 2 else { return nil }
        var directions: [RunDirection] = []
        for id in ids.sorted() {
            let inDirection = trips.filter { $0.directionID == id }
            guard let headsign = mostCommon(inDirection.map(\.headsign)),
                  let ends = mostCommon(inDirection.map { StopPair(first: $0.firstStop, last: $0.lastStop) }) else { return nil }
            directions.append(RunDirection(id: id, headsign: headsign, firstStop: ends.first, lastStop: ends.last))
        }
        return directions
    }

    private struct StopPair: Hashable {
        let first: String
        let last: String
    }

    /// The value most of these give, a tie going to the one that comes first.
    private static func mostCommon<Value: Hashable>(_ values: [Value]) -> Value? {
        var counts: [Value: Int] = [:]
        var order: [Value] = []
        for value in values {
            if counts[value] == nil { order.append(value) }
            counts[value, default: 0] += 1
        }
        // `max` keeps an earlier value over a later equal one.
        return order.max { (counts[$0] ?? 0) < (counts[$1] ?? 0) }
    }
}
