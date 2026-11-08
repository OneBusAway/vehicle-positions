import Foundation
@testable import VehicleTracker

/// ActivityKit as the test plays it: every call is recorded, an update can be
/// held open with `whileUpdating`, and a request fails while Live Activities
/// are off, as ActivityKit's does.
@MainActor final class FakeActivityHost: TripActivityHost {
    enum Refusal: Error {
        case activitiesOff
    }

    struct Request {
        var attributes: TripActivityAttributes
        var state: TripActivityAttributes.ContentState
    }

    struct Update {
        var id: String
        var state: TripActivityAttributes.ContentState
    }

    var areActivitiesEnabled = true
    /// Awaited inside `update`, after it is recorded, so a test can hold an
    /// update in flight.
    var whileUpdating: (() async -> Void)?

    private(set) var requests: [Request] = []
    private(set) var updates: [Update] = []
    private(set) var ends: [String] = []
    /// Whether a second update ever started while one was in flight.
    private(set) var overlapped = false
    private var inFlight = 0

    func request(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState) throws -> String {
        requests.append(Request(attributes: attributes, state: state))
        guard areActivitiesEnabled else { throw Refusal.activitiesOff }
        return "activity-\(requests.count)"
    }

    func update(id: String, _ state: TripActivityAttributes.ContentState) async {
        overlapped = overlapped || inFlight > 0
        inFlight += 1
        updates.append(Update(id: id, state: state))
        await whileUpdating?()
        inFlight -= 1
    }

    func end(id: String) async {
        ends.append(id)
    }
}

/// Holds whoever waits on it until `open()`; from then on nobody waits.
@MainActor final class Gate {
    private var isOpen = false
    private var waiting: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        guard !isOpen else { return }
        await withCheckedContinuation { waiting.append($0) }
    }

    func open() {
        isOpen = true
        for continuation in waiting { continuation.resume() }
        waiting.removeAll()
    }
}
