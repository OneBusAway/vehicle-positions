import Foundation

/// Where the schedule says a vehicle should be: a port of the server's
/// `rider.ScheduledOffsetAt` on absolute times. The scheduled time at an
/// along-shape distance is interpolated between the bracketing stops'
/// departure and arrival, and clamped to the first stop's arrival before it
/// and the last stop's arrival after it.
nonisolated enum ScheduleInterpolator {
    static func scheduledTime(at along: Double, stops: [TripStop]) -> Date? {
        guard let first = stops.first, let last = stops.last else { return nil }
        if along <= first.alongShapeM { return first.arrivalAt }
        if along >= last.alongShapeM { return last.arrivalAt }
        for i in 1..<stops.count {
            let a = stops[i - 1], b = stops[i]
            if along > b.alongShapeM { continue }
            let span = b.alongShapeM - a.alongShapeM
            if span <= 0 { return a.departureAt }
            let fraction = (along - a.alongShapeM) / span
            return a.departureAt.addingTimeInterval(fraction * b.arrivalAt.timeIntervalSince(a.departureAt))
        }
        return last.arrivalAt
    }
}
