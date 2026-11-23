import UIKit
@testable import VehicleTracker

/// Background time a test hands out and takes back by hand, recording what
/// was begun and ended.
@MainActor final class FakeBackgroundTime: BackgroundTime {
    private(set) var begun: [String] = []
    private(set) var ended: [UIBackgroundTaskIdentifier] = []
    private var expiries: [UIBackgroundTaskIdentifier: @MainActor () -> Void] = [:]

    /// Begun and not yet ended.
    var running: Int { expiries.keys.filter { !ended.contains($0) }.count }

    func begin(_ name: String, expired: @escaping @MainActor () -> Void) -> UIBackgroundTaskIdentifier {
        begun.append(name)
        let time = UIBackgroundTaskIdentifier(rawValue: begun.count)
        expiries[time] = expired
        return time
    }

    func end(_ time: UIBackgroundTaskIdentifier) {
        ended.append(time)
    }

    /// What iOS does once the time is up: calls the expiry of each time
    /// still running.
    func expire() {
        for (time, expired) in expiries where !ended.contains(time) {
            expired()
        }
    }
}
