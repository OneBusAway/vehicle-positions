import BackgroundTasks

/// Work an app asks iOS to start later, in the background, once the
/// conditions it names are met: `BGTaskScheduler`, or a test's.
protocol BackgroundTaskScheduler {
    /// Has iOS call `launch` on the main actor each time it starts the task
    /// `identifier`. Only before the app finishes launching, and once per
    /// identifier. False if Info.plist's `BGTaskSchedulerPermittedIdentifiers`
    /// doesn't list `identifier`.
    func register(_ identifier: String, launch: @escaping @MainActor (any BackgroundTaskRun) -> Void) -> Bool
    /// Asks for a run of the task `request` names. A request for that task
    /// still waiting to run is replaced, not added to.
    func submit(_ request: BGTaskRequest) throws
}

/// One run of a background task iOS has started: `BGTask`, or a test's.
protocol BackgroundTaskRun: AnyObject {
    /// Has iOS call `expired` on the main actor shortly before it ends the
    /// run, which must be completed by then.
    func onExpiry(_ expired: @escaping @MainActor () -> Void)
    /// Tells iOS the run is over: false if its work is not all done, after
    /// the next run has been asked for.
    func complete(success: Bool)
}

struct SystemBackgroundTaskScheduler: BackgroundTaskScheduler {
    func register(_ identifier: String, launch: @escaping @MainActor (any BackgroundTaskRun) -> Void) -> Bool {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: identifier, using: .main) { task in
            MainActor.assumeIsolated { launch(task) }
        }
    }

    func submit(_ request: BGTaskRequest) throws {
        try BGTaskScheduler.shared.submit(request)
    }
}

extension BGTask: BackgroundTaskRun {
    func onExpiry(_ expired: @escaping @MainActor () -> Void) {
        // iOS doesn't say which thread it calls this on.
        expirationHandler = { @Sendable in
            Task { @MainActor in expired() }
        }
    }

    func complete(success: Bool) {
        setTaskCompleted(success: success)
    }
}
