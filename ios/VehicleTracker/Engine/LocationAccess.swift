import CoreLocation
import Foundation
import OSLog

/// Whether the phone will give this app positions a trip can report.
nonisolated enum LocationAccessState: Sendable, Equatable {
    case allowed
    /// The driver has not been asked yet.
    case notDetermined
    case denied
    /// Parental controls or a device profile forbid it.
    case restricted
    /// Location Services are off for the whole phone.
    case servicesOff
    /// Allowed, but with Precise Location off: every fix is too coarse to put
    /// the bus on a street (spec §8), so a trip cannot use any of them.
    case approximateOnly
}

/// Checks, and asks for, the location access a trip needs. `TripSession`
/// consults it before telling the server anything, so a refused permission
/// never leaves a trip open on the server.
protocol LocationAccess {
    func current() -> LocationAccessState
    /// Shows the system prompt if the driver has not been asked yet, and
    /// returns the state once they have answered.
    func request() async -> LocationAccessState
    /// Asks once for precise location, with the `TripReporting` purpose
    /// string, and returns the state afterwards.
    func requestPrecise() async -> LocationAccessState
}

/// The shipping ``LocationAccess``. A `CLLocationManager`, not a
/// `CLServiceSession`: a session's precise-location grant lasts only while
/// that session is held, but the manager's temporary grant lasts as long as
/// the trip's background location indicator is up.
final class CoreLocationAccess: NSObject, LocationAccess, CLLocationManagerDelegate {
    static let precisePurposeKey = "TripReporting"

    private let manager = CLLocationManager()
    private var awaitingAnswer: [CheckedContinuation<LocationAccessState, Never>] = []
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "location-access")

    override init() {
        super.init()
        manager.delegate = self
    }

    func current() -> LocationAccessState {
        switch manager.authorizationStatus {
        case .notDetermined: .notDetermined
        case .restricted: .restricted
        // Location Services off for the whole phone also reads as denied;
        // only then is the system-wide check worth making.
        case .denied: CLLocationManager.locationServicesEnabled() ? .denied : .servicesOff
        case .authorizedAlways, .authorizedWhenInUse:
            manager.accuracyAuthorization == .fullAccuracy ? .allowed : .approximateOnly
        @unknown default: .denied
        }
    }

    func request() async -> LocationAccessState {
        guard manager.authorizationStatus == .notDetermined else { return current() }
        return await withCheckedContinuation { continuation in
            awaitingAnswer.append(continuation)
            manager.requestWhenInUseAuthorization()
        }
    }

    func requestPrecise() async -> LocationAccessState {
        do {
            try await manager.requestTemporaryFullAccuracyAuthorization(withPurposeKey: Self.precisePurposeKey)
        } catch {
            log.error("precise location request failed: \(String(describing: error))")
        }
        return current()
    }

    nonisolated func locationManagerDidChangeAuthorization(_: CLLocationManager) {
        // Delivered on the main thread, where the manager was made.
        MainActor.assumeIsolated {
            guard self.manager.authorizationStatus != .notDetermined else { return }
            let state = current()
            let answered = awaitingAnswer
            awaitingAnswer = []
            for continuation in answered { continuation.resume(returning: state) }
        }
    }
}
