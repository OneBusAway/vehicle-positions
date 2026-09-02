import Foundation

/// Polls `predicate` in real time until it holds or `timeout` elapses.
///
/// The reporter's loops run on their own tasks, so a test that has emitted a
/// fix or moved the clock has to wait for the effect rather than assume it. A
/// cancelled caller gives up rather than spinning out the whole timeout.
func poll(timeout: Duration = .seconds(5), until predicate: @Sendable () async -> Bool) async -> Bool {
    let deadline = ContinuousClock.now + timeout
    while ContinuousClock.now < deadline {
        if await predicate() { return true }
        do {
            try await Task.sleep(for: .milliseconds(5))
        } catch {
            return false
        }
    }
    return await predicate()
}
