import UIKit

/// The time iOS gives an app to finish what it started once it leaves the
/// front: `UIApplication`'s background tasks, or a test's.
protocol BackgroundTime {
    /// Asks for time to finish `name`. iOS calls `expired` on the main actor
    /// just before it takes the time back, and the time must be ended by
    /// then. `.invalid` when there is none to give.
    func begin(_ name: String, expired: @escaping @MainActor () -> Void) -> UIBackgroundTaskIdentifier
    func end(_ time: UIBackgroundTaskIdentifier)
}

struct ApplicationBackgroundTime: BackgroundTime {
    func begin(_ name: String, expired: @escaping @MainActor () -> Void) -> UIBackgroundTaskIdentifier {
        UIApplication.shared.beginBackgroundTask(withName: name, expirationHandler: expired)
    }

    func end(_ time: UIBackgroundTaskIdentifier) {
        UIApplication.shared.endBackgroundTask(time)
    }
}
