import Foundation

/// Polls until the condition holds or `seconds` pass. Async work started
/// by a session runs on the main actor between polls.
@MainActor func eventually(within seconds: TimeInterval = 2, _ condition: @MainActor () -> Bool) async -> Bool {
    let deadline = Date().addingTimeInterval(seconds)
    while Date() < deadline {
        if condition() { return true }
        try? await Task.sleep(for: .milliseconds(10))
    }
    return condition()
}
