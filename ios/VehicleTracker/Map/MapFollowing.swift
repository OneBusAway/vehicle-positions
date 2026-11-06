import Observation
import UIKit
import VehiclePositionsKit

/// Whether the map follows the vehicle, and Android's rule for giving it
/// back: a pan or pinch stops following and shows Re-center; 10 s after the
/// last touch ends the map follows again; Re-center follows at once. While
/// VoiceOver runs there is no timed return, because a timer must not undo a
/// VoiceOver user's exploring; Re-center still works.
@MainActor @Observable
final class MapFollowing {
    static let returnDelay: Duration = .seconds(10)

    private(set) var isFollowing = true

    private let clock: any RideClock
    private let isVoiceOverRunning: () -> Bool
    /// The one timed return in flight. Cancelled by a new touch, by
    /// Re-center, and when Tracking leaves the screen.
    private var returnTask: Task<Void, Never>?

    init(clock: any RideClock = ContinuousRideClock(), isVoiceOverRunning: @escaping () -> Bool = { UIAccessibility.isVoiceOverRunning }) {
        self.clock = clock
        self.isVoiceOverRunning = isVoiceOverRunning
    }

    /// The driver started dragging or pinching the map.
    func userMovedMap() {
        cancelReturn()
        isFollowing = false
    }

    /// The driver's touch ended: the map follows again in 10 s, unless they
    /// touch it again first.
    func userTouchEnded() {
        cancelReturn()
        guard !isFollowing, !isVoiceOverRunning() else { return }
        returnTask = Task { [weak self, clock] in
            do {
                try await clock.sleep(for: Self.returnDelay)
            } catch {
                // Cancelled: a newer touch, Re-center or leaving the screen
                // took over, and there is nothing to clean up.
                return
            }
            self?.follow()
        }
    }

    /// Re-center, or the timed return.
    func follow() {
        cancelReturn()
        isFollowing = true
    }

    /// Leaving Tracking: no return fires into a screen that is gone.
    func stop() {
        cancelReturn()
    }

    private func cancelReturn() {
        returnTask?.cancel()
        returnTask = nil
    }
}
