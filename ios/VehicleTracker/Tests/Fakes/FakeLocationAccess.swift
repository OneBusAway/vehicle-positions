import Foundation
@testable import VehicleTracker

/// Location access scripted by the test. `state` is what `current()` reports;
/// a request answers with its scripted state, as the driver would at the
/// prompt, or leaves `state` as it was when nothing is scripted, as a refused
/// prompt does. Every request is counted.
final class FakeLocationAccess: LocationAccess {
    var state: LocationAccessState = .allowed
    var requestAnswer: LocationAccessState?
    var preciseAnswer: LocationAccessState?
    /// When set, the next `request()` waits for `answer()`, so a test can
    /// look at the session while the prompt is up. Only that one waits: a
    /// second request that should never have been made fails the test rather
    /// than hanging it.
    var holdsRequest = false

    private(set) var requests = 0
    private(set) var preciseRequests = 0
    private var held: CheckedContinuation<Void, Never>?

    func current() -> LocationAccessState { state }

    func request() async -> LocationAccessState {
        requests += 1
        if holdsRequest {
            holdsRequest = false
            await withCheckedContinuation { held = $0 }
        }
        if let requestAnswer { state = requestAnswer }
        return state
    }

    func requestPrecise() async -> LocationAccessState {
        preciseRequests += 1
        if let preciseAnswer { state = preciseAnswer }
        return state
    }

    var isHoldingRequest: Bool { held != nil }

    func answer() {
        held?.resume()
        held = nil
    }
}
