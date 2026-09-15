import Foundation

/// Polls until the condition holds or two seconds pass. Async work started
/// by a session runs on the main actor between polls.
@MainActor func eventually(_ condition: @MainActor () -> Bool) async -> Bool {
    let deadline = Date().addingTimeInterval(2)
    while Date() < deadline {
        if condition() { return true }
        try? await Task.sleep(for: .milliseconds(10))
    }
    return condition()
}
