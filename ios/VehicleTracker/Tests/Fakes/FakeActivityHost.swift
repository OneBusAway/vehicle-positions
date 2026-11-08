import ActivityKit
import Foundation
@testable import VehicleTracker

/// ActivityKit as the test plays it: every call is recorded, an update can be
/// held open with `whileUpdating`, and a request fails while Live Activities
/// are off, or from the background, as ActivityKit's does. Activities keep an
/// `ActivityState`, judged live by the app's own `ActivityKitHost.isLive`.
@MainActor final class FakeActivityHost: TripActivityHost {
    enum Refusal: Error {
        case activitiesOff
    }

    struct Request {
        var attributes: TripActivityAttributes
        var state: TripActivityAttributes.ContentState
        var staleDate: Date
    }

    struct Update {
        var id: String
        var state: TripActivityAttributes.ContentState
        var staleDate: Date
    }

    struct Listed {
        var id: String
        var serverTripID: Int64
        var state: ActivityState
    }

    var areActivitiesEnabled = true
    /// Off while the phone app is in the background: a request is refused.
    var inForeground = true
    /// What ActivityKit lists: set by a test as a relaunch finds it, and
    /// added to by every request.
    var listed: [Listed] = []
    /// Awaited inside `update`, after it is recorded, so a test can hold an
    /// update in flight.
    var whileUpdating: (() async -> Void)?

    private(set) var requests: [Request] = []
    private(set) var updates: [Update] = []
    private(set) var ends: [String] = []
    /// Whether a second update ever started while one was in flight.
    private(set) var overlapped = false
    private var inFlight = 0
    private var watchers: [String: AsyncStream<Void>.Continuation] = [:]

    func existing() -> [TripActivitySnapshot] {
        listed.map { TripActivitySnapshot(id: $0.id, serverTripID: $0.serverTripID, isLive: ActivityKitHost.isLive($0.state)) }
    }

    func request(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState, staleDate: Date) throws -> String {
        requests.append(Request(attributes: attributes, state: state, staleDate: staleDate))
        guard areActivitiesEnabled else { throw Refusal.activitiesOff }
        guard inForeground else { throw TripActivityError.notInForeground }
        let id = "activity-\(requests.count)"
        listed.append(Listed(id: id, serverTripID: attributes.serverTripID, state: .active))
        return id
    }

    func update(id: String, _ state: TripActivityAttributes.ContentState, staleDate: Date) async {
        overlapped = overlapped || inFlight > 0
        inFlight += 1
        updates.append(Update(id: id, state: state, staleDate: staleDate))
        await whileUpdating?()
        inFlight -= 1
    }

    func end(id: String) async {
        ends.append(id)
    }

    func endings(of id: String) -> AsyncStream<Void> {
        let (stream, continuation) = AsyncStream.makeStream(of: Void.self)
        watchers[id] = continuation
        return stream
    }

    /// The driver swipes the activity away, or the system ends it: whoever
    /// watches it hears, unless the new state still counts as live.
    func set(_ state: ActivityState, of id: String) {
        guard let index = listed.firstIndex(where: { $0.id == id }) else { return }
        listed[index].state = state
        if !ActivityKitHost.isLive(state) { watchers[id]?.yield() }
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
