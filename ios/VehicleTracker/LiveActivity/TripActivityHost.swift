import ActivityKit
import Foundation
import OSLog

/// What `TripActivityController` asks of ActivityKit, in ids and values. An
/// `Activity` never crosses this line: it is not Sendable, so it must not be
/// held across a task (OneBusAway iOS #1243), and each call finds it again by
/// its id.
@MainActor protocol TripActivityHost: AnyObject {
    /// Whether the driver allows this app's Live Activities.
    var areActivitiesEnabled: Bool { get }
    /// Starts an activity and returns its id.
    func request(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState) throws -> String
    func update(id: String, _ state: TripActivityAttributes.ContentState) async
    /// Ends the activity and takes it off the screen at once.
    func end(id: String) async
}

/// The one place the app calls ActivityKit. It has no tests because it is
/// ActivityKit: `Activity.request` can't run in a unit test. Keeping every
/// call in this one small type, each finding its activity by id, is what
/// stands in for that coverage, as `Activity.requestProminent` does in
/// OneBusAway iOS.
@MainActor final class ActivityKitHost: TripActivityHost {
    nonisolated private static let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "live-activity")

    var areActivitiesEnabled: Bool { ActivityAuthorizationInfo().areActivitiesEnabled }

    /// No push token: the app runs in the background for location through
    /// the whole trip and is the only thing that knows the next position, so
    /// it updates the activity itself.
    func request(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState) throws -> String {
        try Activity.request(attributes: attributes, content: ActivityContent(state: state, staleDate: nil), pushType: nil).id
    }

    func update(id: String, _ state: TripActivityAttributes.ContentState) async {
        await Self.update(id: id, state)
    }

    func end(id: String) async {
        await Self.end(id: id)
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

    nonisolated private static func activity(_ id: String) -> Activity<TripActivityAttributes>? {
        Activity<TripActivityAttributes>.activities.first { $0.id == id }
    }
}
