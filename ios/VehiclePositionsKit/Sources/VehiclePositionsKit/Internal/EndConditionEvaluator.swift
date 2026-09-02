import Foundation

/// Decides, sample by sample, whether a ride should stop (spec §4.4). Duration
/// and upload-failure limits are the reporter's business; this type only knows
/// what the location stream can tell it.
struct EndConditionEvaluator: Sendable {
    private let configuration: RideReporterConfiguration
    private let destination: Coordinate?

    /// The first fix seen, which anchors the travel guard below.
    private(set) var firstFix: Coordinate?
    /// When the current run of stationary samples began.
    private(set) var stationarySince: Date?

    init(configuration: RideReporterConfiguration, destination: Coordinate?) {
        self.configuration = configuration
        self.destination = destination
    }

    mutating func evaluate(_ sample: LocationSample, now: Date) -> RideEndReason? {
        // A ride that cannot get positions is over, whatever the last fix said.
        switch sample.diagnostic {
        case .authorizationDenied: return .authorizationDenied
        case .locationUnavailable: return .locationUnavailable
        case .accuracyLimited, .insufficientlyInUse, nil: break
        }

        if sample.isStationary {
            let since = stationarySince ?? now
            stationarySince = since
            if now.timeIntervalSince(since) >= configuration.stationaryTimeout.timeInterval {
                return .stationary
            }
        } else if sample.fix != nil {
            // Only a real fix is evidence of movement. A diagnostic-only sample
            // says nothing about whether the device moved, so it must not hand
            // a parked bus another full stationary timeout.
            stationarySince = nil
        }

        guard let fix = sample.fix else { return nil }
        let position = fix.coordinate
        let origin = firstFix ?? position
        firstFix = origin

        // Arrival needs both proximity to the destination and a real journey to
        // it: boarding a stop away from where you started must not end the ride.
        guard let destination else { return nil }
        guard GeoDistance.meters(from: position, to: destination) <= configuration.arrivalRadiusMeters else { return nil }
        guard GeoDistance.meters(from: origin, to: position) >= configuration.minimumTravelBeforeArrivalMeters else { return nil }
        return .arrived
    }
}
