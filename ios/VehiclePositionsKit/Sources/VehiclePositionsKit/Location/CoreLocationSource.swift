#if os(iOS)
import CoreLocation
import Foundation

/// The shipping ``LocationSource``: Core Location's `liveUpdates` sequence,
/// held open by a service session for as long as anyone is iterating it.
public final class CoreLocationSource: LocationSource {
    /// A plain case-only enum Core Location has not marked `Sendable`; it is
    /// immutable and carries nothing, so sharing it is safe.
    nonisolated(unsafe) private let configuration: CLLocationUpdate.LiveConfiguration

    /// `otherNavigation` is the profile for a rider on a vehicle they are not
    /// driving: frequent updates, no automotive map-matching.
    public init(configuration: CLLocationUpdate.LiveConfiguration = .otherNavigation) {
        self.configuration = configuration
    }

    public func updates() -> AsyncThrowingStream<LocationSample, any Error> {
        // Built before the iterating task: `Updates` is `Sendable`, the
        // configuration it was built from is not.
        let liveUpdates = CLLocationUpdate.liveUpdates(configuration)
        return AsyncThrowingStream { continuation in
            // Without a session Core Location delivers nothing, and the session
            // must outlive the first await — so the iteration owns it and
            // termination gives it back.
            let session = CLServiceSession(authorization: .whenInUse)
            let task = Task {
                do {
                    for try await update in liveUpdates {
                        continuation.yield(LocationSample(update))
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in
                task.cancel()
                session.invalidate()
            }
        }
    }

    public func beginBackgroundActivity() -> any BackgroundActivityHandle {
        Handle(session: CLBackgroundActivitySession())
    }

    /// Owns the session that keeps location running in the background — and
    /// puts the blue indicator on the rider's screen while it lives.
    private struct Handle: BackgroundActivityHandle {
        let session: CLBackgroundActivitySession

        func invalidate() {
            session.invalidate()
        }
    }
}

extension LocationSample {
    /// Maps one Core Location update onto the SDK's own sample type.
    init(_ update: CLLocationUpdate) {
        self.init(
            location: update.location,
            stationary: update.stationary,
            diagnostic: Self.diagnostic(for: update)
        )
    }

    /// The mapping itself, taken apart from `CLLocationUpdate` — which cannot be
    /// constructed outside Core Location — so that tests can reach it.
    init(location: CLLocation?, stationary: Bool, diagnostic: LocationDiagnostic?) {
        self.init(
            fix: location.map {
                LocationFix(
                    latitude: $0.coordinate.latitude,
                    longitude: $0.coordinate.longitude,
                    horizontalAccuracy: $0.horizontalAccuracy,
                    speed: $0.speed,
                    course: $0.course,
                    timestamp: $0.timestamp
                )
            },
            isStationary: stationary,
            diagnostic: diagnostic
        )
    }

    /// Worst first: an update can report several conditions at once, and the
    /// ride only acts on the most serious one.
    private static func diagnostic(for update: CLLocationUpdate) -> LocationDiagnostic? {
        if update.authorizationDenied || update.authorizationDeniedGlobally || update.authorizationRestricted {
            return .authorizationDenied
        }
        if update.locationUnavailable { return .locationUnavailable }
        if update.accuracyLimited { return .accuracyLimited }
        if update.insufficientlyInUse { return .insufficientlyInUse }
        return nil
    }
}
#endif
