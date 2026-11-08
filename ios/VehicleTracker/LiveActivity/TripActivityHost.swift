import ActivityKit
import Foundation
import OSLog

/// An activity as ActivityKit lists it.
nonisolated struct TripActivitySnapshot: Equatable {
    var id: String
    var serverTripID: Int64
    /// Neither ended nor dismissed.
    var isLive: Bool
}

nonisolated enum TripActivityError: Error {
    /// ActivityKit starts a Live Activity only while the app is in front:
    /// a trip started from the car, with the phone app in the background.
    case notInForeground
}

/// What `TripActivityController` asks of ActivityKit, in ids and values. An
/// `Activity` never crosses this line: it is not Sendable, so it must not be
/// held across a task (OneBusAway iOS #1243), and each call finds it again by
/// its id.
@MainActor protocol TripActivityHost: AnyObject {
    /// Whether the driver allows this app's Live Activities.
    var areActivitiesEnabled: Bool { get }
    /// Every activity ActivityKit still lists, live or not: a dismissed one
    /// stays listed until the system clears it.
    func existing() -> [TripActivitySnapshot]
    /// Starts an activity and returns its id. Throws
    /// `TripActivityError.notInForeground` for ActivityKit's `.visibility`.
    func request(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState) throws -> String
    func update(id: String, _ state: TripActivityAttributes.ContentState) async
    /// Ends the activity and takes it off the screen at once.
    func end(id: String) async
    /// Yields once when the activity stops being live by someone else's
    /// hand: the driver swiped it away, or the system ended it.
    func endings(of id: String) -> AsyncStream<Void>
}

/// The one place the app calls ActivityKit. It has no tests because it is
/// ActivityKit: `Activity.request` can't run in a unit test. Keeping every
/// call in this one small type, each finding its activity by id, is what
/// stands in for that coverage, as `Activity.requestProminent` does in
/// OneBusAway iOS.
@MainActor final class ActivityKitHost: TripActivityHost {
    nonisolated private static let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "live-activity")

    /// OneBusAway iOS's rule (`LiveActivityRegistry.isLive`): only ended and
    /// dismissed count as gone, so a state a later iOS adds counts as live.
    nonisolated static func isLive(_ state: ActivityState) -> Bool {
        state != .ended && state != .dismissed
    }

    var areActivitiesEnabled: Bool { ActivityAuthorizationInfo().areActivitiesEnabled }

    func existing() -> [TripActivitySnapshot] {
        Activity<TripActivityAttributes>.activities.map {
            TripActivitySnapshot(id: $0.id, serverTripID: $0.attributes.serverTripID, isLive: Self.isLive($0.activityState))
        }
    }

    /// No push token: the app runs in the background for location through
    /// the whole trip and is the only thing that knows the next position, so
    /// it updates the activity itself.
    func request(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState) throws -> String {
        do {
            return try Activity.request(attributes: attributes, content: ActivityContent(state: state, staleDate: nil), pushType: nil).id
        } catch ActivityAuthorizationError.visibility {
            throw TripActivityError.notInForeground
        }
    }

    func update(id: String, _ state: TripActivityAttributes.ContentState) async {
        await Self.update(id: id, state)
    }

    func end(id: String) async {
        await Self.end(id: id)
    }

    func endings(of id: String) -> AsyncStream<Void> {
        let (stream, continuation) = AsyncStream.makeStream(of: Void.self)
        let watch = Task {
            await Self.waitUntilGone(id)
            continuation.yield()
            continuation.finish()
        }
        continuation.onTermination = { _ in watch.cancel() }
        return stream
    }

    // An `Activity` is not Sendable, so it is found and used inside one
    // nonisolated function and never handed across.

    @concurrent nonisolated private static func update(id: String, _ state: TripActivityAttributes.ContentState) async {
        guard let activity = activity(id) else {
            log.notice("Live Activity \(id, privacy: .public) is gone; the update was dropped")
            return
        }
        await activity.update(ActivityContent(state: state, staleDate: nil))
    }

    @concurrent nonisolated private static func end(id: String) async {
        guard let activity = activity(id) else {
            log.notice("Live Activity \(id, privacy: .public) was already gone when its trip ended")
            return
        }
        await activity.end(nil, dismissalPolicy: .immediate)
    }

    /// Returns once the activity is ended or dismissed, or at once if it is
    /// no longer listed.
    @concurrent nonisolated private static func waitUntilGone(_ id: String) async {
        guard let activity = activity(id) else { return }
        for await state in activity.activityStateUpdates where !isLive(state) {
            return
        }
    }

    nonisolated private static func activity(_ id: String) -> Activity<TripActivityAttributes>? {
        Activity<TripActivityAttributes>.activities.first { $0.id == id }
    }
}
