import Foundation
import VehiclePositionsKit

/// How the driver is keeping to the route and the schedule, judged from one
/// position fix.
nonisolated enum AdherenceStatus: Sendable, Equatable {
    case onTime, early, late, offSchedule, offRoute
}

nonisolated struct Adherence: Sendable, Equatable {
    var fix: LocationFix
    var projection: Projection
    var isOnRoute: Bool
    /// Where the schedule says the vehicle should be at this along-shape position.
    var scheduledTime: Date
    /// Seconds behind schedule; negative is early.
    var scheduleDeviation: TimeInterval
    var nextStop: TripStop
    /// Metres along the shape to the next stop.
    var distanceToNextStop: Double
    /// Seconds to the next stop at the current speed.
    var timeToNextStop: TimeInterval
    var status: AdherenceStatus
}

/// Judges each fix against the trip's shape and schedule with the rules the
/// server's rider engine applies (spec §5.5): the on-route tolerance is the
/// server's shape distance plus the fix's own accuracy, and the schedule
/// window is the server's. The display cut-offs for early and late are this
/// app's own.
nonisolated struct AdherenceEvaluator: Sendable {
    /// More than a minute ahead of schedule shows as early.
    static let earlyThreshold: TimeInterval = -60
    /// More than five minutes behind shows as late.
    static let lateThreshold: TimeInterval = 300
    /// Speed floor for the time-to-next-stop estimate, so a stopped or
    /// speed-less fix still yields a finite time.
    static let minimumSpeed = 3.0

    let trip: TripGeometry
    let shape: ShapeGeometry

    /// Nil for a trip with no usable shape or no stops: nothing to judge against.
    init?(trip: TripGeometry) {
        guard let shape = ShapeGeometry(points: trip.shapePoints), !trip.stops.isEmpty else { return nil }
        self.trip = trip
        self.shape = shape
    }

    func evaluate(_ fix: LocationFix, previous: Adherence?) -> Adherence {
        // The previous match keeps loops and out-and-backs from snapping to
        // the wrong pass; an off-route fix says nothing about where on the
        // shape the vehicle is, so it does not advance the hint.
        let hint = (previous?.isOnRoute == true) ? previous?.projection.alongShape : nil
        let projection = shape.project(GeoPoint(fix.latitude, fix.longitude), hint: hint)

        let tolerance = trip.thresholds.maxShapeDistanceM + max(fix.horizontalAccuracy, 0)
        let isOnRoute = projection.distanceToShape <= tolerance

        let scheduledTime = ScheduleInterpolator.scheduledTime(at: projection.alongShape, stops: trip.stops) ?? fix.timestamp
        let deviation = fix.timestamp.timeIntervalSince(scheduledTime)

        let nextStop = trip.stops.first { $0.alongShapeM > projection.alongShape } ?? trip.stops[trip.stops.count - 1]
        let distance = max(0, nextStop.alongShapeM - projection.alongShape)
        let time = distance / max(fix.speed, Self.minimumSpeed)

        let status: AdherenceStatus
        switch true {
        case !isOnRoute:
            status = .offRoute
        case deviation < -Double(trip.thresholds.scheduleEarlyS), deviation > Double(trip.thresholds.scheduleLateS):
            status = .offSchedule
        case deviation < Self.earlyThreshold:
            status = .early
        case deviation > Self.lateThreshold:
            status = .late
        default:
            status = .onTime
        }

        return Adherence(
            fix: fix, projection: projection, isOnRoute: isOnRoute,
            scheduledTime: scheduledTime, scheduleDeviation: deviation,
            nextStop: nextStop, distanceToNextStop: distance, timeToNextStop: time,
            status: status
        )
    }
}
