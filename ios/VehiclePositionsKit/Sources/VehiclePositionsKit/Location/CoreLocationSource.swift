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
            // must outlive the first await — so the subscription owns it and
            // termination gives it back.
            let subscription = Subscription(session: CLServiceSession(authorization: .whenInUse))
            // Installed before the task exists: a stream torn down in between
            // would otherwise hold the session with nothing left to release it.
            continuation.onTermination = { _ in subscription.cancel() }
            subscription.begin(Task {
                do {
                    for try await update in liveUpdates {
                        continuation.yield(LocationSample(update))
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            })
        }
    }

    public func beginBackgroundActivity() -> any BackgroundActivityHandle {
        Handle(session: CLBackgroundActivitySession())
    }

    /// Holds the service session and the iterating task for one subscription,
    /// so termination releases both whichever order the two arrive in.
    private final class Subscription: @unchecked Sendable {
        private let lock = NSLock()
        private let session: CLServiceSession
        private var task: Task<Void, Never>?
        private var cancelled = false

        init(session: CLServiceSession) {
            self.session = session
        }

        /// Adopts the iterating task, or cancels it outright if termination
        /// already came and went.
        func begin(_ task: Task<Void, Never>) {
            let tooLate = lock.withLock {
                if cancelled { return true }
                self.task = task
                return false
            }
            if tooLate { task.cancel() }
        }

        func cancel() {
            let running = lock.withLock {
                cancelled = true
                defer { task = nil }
                return task
            }
            running?.cancel()
            session.invalidate()
        }
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
