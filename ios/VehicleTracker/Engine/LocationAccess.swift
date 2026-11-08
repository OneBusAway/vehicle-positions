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
    /// returns the state once they have answered, or after
    /// ``CoreLocationAccess/promptWait`` without an answer, when the prompt
    /// may never have shown.
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
    /// Long enough to read a two-button prompt, and short enough that a start
    /// from the car does not sit on Starting… for long.
    static let promptWait: Duration = .seconds(30)

    private let manager = CLLocationManager()
    private let prompts = PromptWait()
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "location-access")

    override init() {
        super.init()
        manager.delegate = self
    }

    func current() -> LocationAccessState {
        Self.state(status: manager.authorizationStatus, accuracy: manager.accuracyAuthorization,
                   servicesEnabled: CLLocationManager.locationServicesEnabled)
    }

    /// What a trip can count on, from what iOS says. Location Services off
    /// outranks the app's own answer: no prompt can fix it. `servicesEnabled`
    /// is read only when it matters, since Apple warns it can block.
    nonisolated static func state(status: CLAuthorizationStatus, accuracy: CLAccuracyAuthorization,
                                  servicesEnabled: () -> Bool) -> LocationAccessState {
        switch status {
        case .notDetermined: servicesEnabled() ? .notDetermined : .servicesOff
        case .restricted: .restricted
        case .denied: servicesEnabled() ? .denied : .servicesOff
        case .authorizedAlways, .authorizedWhenInUse: accuracy == .fullAccuracy ? .allowed : .approximateOnly
        @unknown default: .denied
        }
    }

    func request() async -> LocationAccessState {
        let state = current()
        guard state == .notDetermined else { return state }
        return await prompts.wait(upTo: Self.promptWait, read: current) { manager.requestWhenInUseAuthorization() }
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
            prompts.answer(current())
        }
    }
}

/// Callers waiting on the location prompt. Each is released once: by the
/// answer, or by its own deadline with what iOS says then, because a prompt
/// can fail to show (from the car, with the phone app in the background) and
/// then no answer ever comes.
final class PromptWait {
    private struct Waiter {
        let continuation: CheckedContinuation<LocationAccessState, Never>
        let deadline: Task<Void, Never>
    }

    private let sleep: (Duration) async throws -> Void
    private var waiters: [Int: Waiter] = [:]
    private var nextID = 0
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "location-access")

    init(sleep: @escaping (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.sleep = sleep
    }

    /// Registers the caller before `ask` runs, so an answer that comes at
    /// once still finds it.
    func wait(upTo limit: Duration, read: @escaping () -> LocationAccessState,
              ask: () -> Void) async -> LocationAccessState {
        await withCheckedContinuation { continuation in
            let id = nextID
            nextID += 1
            let deadline = Task {
                do { try await sleep(limit) } catch { return }
                guard let waiter = waiters.removeValue(forKey: id) else { return }
                let state = read()
                log.notice("no answer to the location prompt after \(limit, privacy: .public); going on with \(String(describing: state), privacy: .public)")
                waiter.continuation.resume(returning: state)
            }
            waiters[id] = Waiter(continuation: continuation, deadline: deadline)
            ask()
        }
    }

    func answer(_ state: LocationAccessState) {
        let answered = waiters.values
        waiters = [:]
        for waiter in answered {
            waiter.deadline.cancel()
            waiter.continuation.resume(returning: state)
        }
    }
}
