import Foundation
import Observation

/// Where the welcome is: which screen shows, which way it came, and how far
/// that screen's picture has played. One timeline for each showing of a
/// screen, stepped through `sleep`, so a test's clock can drive it. What
/// moves on a beat is animated where it is drawn, never from here.
@MainActor @Observable
final class WelcomeFlow {
    private(set) var step = WelcomeStep.riders
    /// The last move was Next rather than back: the way the screens slide.
    private(set) var isForward = true
    /// Counts the screens shown. Each showing is a view of its own, so the
    /// one before it leaves as it was and a screen come back to starts clean.
    private(set) var showing = 0
    private var played = WelcomePicture.riders(.start)

    @ObservationIgnored private var playing: Task<Void, Never>?
    private let sleep: (Duration) async throws -> Void

    /// The real wait asks for no tolerance: the system's default lets each
    /// beat slip a few hundredths of a second, and eleven beats add up.
    init(sleep: @escaping (Duration) async throws -> Void = { try await Task.sleep(for: $0, tolerance: .zero) }) {
        self.sleep = sleep
    }

    var isFirst: Bool { step == WelcomeStep.allCases.first }
    var isLast: Bool { step == WelcomeStep.allCases.last }

    /// What the showing screen's picture has reached. With `still`, Reduce
    /// Motion's version, it is at rest from its first frame.
    func picture(still: Bool) -> WelcomePicture {
        still ? step.timeline[step.timeline.count - 1].picture : played
    }

    /// Plays the showing screen's picture from the start: the welcome has
    /// come onto the screen.
    func start() {
        play()
    }

    /// Stops the picture where it is: the welcome has left the screen.
    func stop() {
        playing?.cancel()
        playing = nil
    }

    /// On to the next screen, whose picture plays from the start. Nothing
    /// follows the last one: its button ends the welcome.
    func next() {
        guard let next = WelcomeStep(rawValue: step.rawValue + 1) else { return }
        show(next, forward: true)
    }

    /// Back to the screen before, whose picture plays again from the start.
    func back() {
        guard let previous = WelcomeStep(rawValue: step.rawValue - 1) else { return }
        show(previous, forward: false)
    }

    private func show(_ step: WelcomeStep, forward: Bool) {
        self.step = step
        isForward = forward
        showing += 1
        play()
    }

    /// Reaches each beat of the showing screen's timeline when its time
    /// comes. Each wait is counted from the beat before, so a late beat
    /// carries the rest with it and none overtakes another. A move to
    /// another screen cancels what is left.
    private func play() {
        playing?.cancel()
        let timeline = step.timeline
        played = timeline[0].picture
        playing = Task { [sleep] in
            for (last, beat) in zip(timeline, timeline.dropFirst()) {
                do {
                    try await sleep(beat.at - last.at)
                } catch {
                    return
                }
                // A wait can end just as the driver taps Next: it returns
                // without an error, after the next screen has started.
                guard !Task.isCancelled else { return }
                played = beat.picture
            }
        }
    }
}
