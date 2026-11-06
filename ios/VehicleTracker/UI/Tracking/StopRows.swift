import Foundation

/// The trip sheet's stop list, decided apart from the views so every shape
/// of it is checked without a device: Android's `stopRows`, with the stops
/// before the last one passed folded into one row, as OneBusAway iOS folds
/// a run of passed stops on its trip page.
enum StopRows {
    enum Kind: Equatable {
        case passed, next, upcoming, last
    }

    enum Row: Equatable, Identifiable {
        case stop(TripStop, Kind)
        /// Stands in for the `count` passed stops above the last one passed.
        case folded(count: Int)
        /// The vehicle itself, between the stop it last passed and the next.
        case vehicle

        /// A loop route calls at the same stop twice, so a stop's id alone
        /// would repeat; its sequence tells the two calls apart.
        var id: String {
            switch self {
            case .stop(let stop, _): "\(stop.id)#\(stop.sequence)"
            case .folded: "folded"
            case .vehicle: "vehicle"
            }
        }
    }

    /// Before the first fix `nextStopIndex` is nil and there is no vehicle to
    /// place: the whole run, from its first stop. After it: the folded row
    /// (or, `unfolded`, the stops it stood for), the last passed stop, the
    /// vehicle, the next stop, and the rest to the end of the line.
    static func rows(stops: [TripStop], nextStopIndex: Int?, unfolded: Bool) -> [Row] {
        guard let next = nextStopIndex, stops.indices.contains(next) else {
            return stops.indices.map { .stop(stops[$0], $0 == 0 ? .next : upcoming($0, in: stops)) }
        }
        var rows: [Row] = []
        let hidden = next - 1
        if hidden > 0 {
            rows += unfolded ? stops[..<hidden].map { .stop($0, .passed) } : [.folded(count: hidden)]
        }
        if next > 0 { rows.append(.stop(stops[next - 1], .passed)) }
        rows.append(.vehicle)
        rows.append(.stop(stops[next], .next))
        rows += stops.indices.dropFirst(next + 1).map { .stop(stops[$0], upcoming($0, in: stops)) }
        return rows
    }

    private static func upcoming(_ index: Int, in stops: [TripStop]) -> Kind {
        index == stops.count - 1 ? .last : .upcoming
    }
}
