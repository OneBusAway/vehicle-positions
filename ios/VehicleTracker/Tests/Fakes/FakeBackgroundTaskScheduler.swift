import BackgroundTasks
import Foundation
import Testing
@testable import VehicleTracker

/// iOS's background task scheduler as a test drives it. It permits what the
/// app's Info.plist lists, keeps one request waiting per task (a new one
/// replaces it, as `BGTaskScheduler` does), and starts a run when the test
/// says the request's conditions are met.
@MainActor final class FakeBackgroundTaskScheduler: BackgroundTaskScheduler {
    private let permitted: [String]
    private var launches: [String: @MainActor (any BackgroundTaskRun) -> Void] = [:]
    /// Every request submitted, in order, those refused too.
    private(set) var submitted: [BGTaskRequest] = []
    /// The request waiting to run, per task.
    private(set) var waiting: [String: BGTaskRequest] = [:]
    /// What the app told the scheduler, in order: "submit", "complete(true)".
    private(set) var events: [String] = []
    /// Thrown by each submit from now on, as iOS refuses one.
    var submitError: (any Error)?

    var registered: [String] { Array(launches.keys) }

    init(permitted: [String] = Bundle.main.object(forInfoDictionaryKey: "BGTaskSchedulerPermittedIdentifiers") as? [String] ?? []) {
        self.permitted = permitted
    }

    func register(_ identifier: String, launch: @escaping @MainActor (any BackgroundTaskRun) -> Void) -> Bool {
        guard permitted.contains(identifier) else { return false }
        #expect(launches[identifier] == nil, "iOS kills an app that registers a task twice")
        launches[identifier] = launch
        return true
    }

    func submit(_ request: BGTaskRequest) throws {
        submitted.append(request)
        events.append("submit")
        guard permitted.contains(request.identifier) else { throw BGTaskScheduler.Error(.notPermitted) }
        if let submitError { throw submitError }
        waiting[request.identifier] = request
    }

    /// What iOS does once the waiting request's conditions are met: takes it
    /// off the list and starts a run. Nil when nothing is waiting.
    func run(_ identifier: String) -> FakeBackgroundTaskRun? {
        guard waiting.removeValue(forKey: identifier) != nil, let launch = launches[identifier] else { return nil }
        let run = FakeBackgroundTaskRun { [weak self] success in self?.events.append("complete(\(success))") }
        launch(run)
        return run
    }
}

/// One run of a background task, ended by hand.
@MainActor final class FakeBackgroundTaskRun: BackgroundTaskRun {
    private var expired: (@MainActor () -> Void)?
    private let completed: (Bool) -> Void
    /// Each `complete(success:)`, in order.
    private(set) var completions: [Bool] = []

    init(completed: @escaping (Bool) -> Void) {
        self.completed = completed
    }

    func onExpiry(_ expired: @escaping @MainActor () -> Void) {
        self.expired = expired
    }

    func complete(success: Bool) {
        completions.append(success)
        completed(success)
    }

    /// What iOS does as it ends the run: calls the expiry handler once and
    /// clears it. After `complete(success:)`, an expiry iOS called as the
    /// run completed, landing late.
    func expire() {
        guard let expired else {
            Issue.record("iOS marks a run with no expiry handler failed")
            return
        }
        self.expired = nil
        expired()
    }
}
