import Foundation

/// The passage of time, as the ride reporter sees it. Production uses
/// ``ContinuousRideClock``; tests drive ``ManualRideClock`` so a three-hour ride
/// takes microseconds and never depends on wall-clock scheduling.
public protocol RideClock: Sendable {
    /// The current instant.
    var now: Date { get }
    /// Suspends until `duration` has passed, throwing `CancellationError` if the
    /// calling task is cancelled first.
    func sleep(for duration: Duration) async throws
}

/// The real clock: `Date()` and `Task.sleep(for:)`.
public struct ContinuousRideClock: RideClock {
    public init() {}

    public var now: Date { Date() }

    public func sleep(for duration: Duration) async throws {
        try await Task.sleep(for: duration)
    }
}

/// A clock that only moves when a test says so. Sleepers park until
/// ``advance(by:)`` carries `now` past their deadline.
public final class ManualRideClock: RideClock, @unchecked Sendable {
    /// Where a single `sleep(for:)` call has got to.
    private enum SleepState {
        /// `sleep` has begun but has not yet handed over its continuation.
        case pending
        /// Parked, waiting for the clock to reach `deadline`.
        case parked(deadline: Date, continuation: CheckedContinuation<Void, any Error>)
        /// Cancelled before the continuation was installed.
        case cancelled
    }

    private let lock = NSLock()
    private var currentTime: Date
    private var states: [Int: SleepState] = [:]
    private var nextID = 0

    public init(now: Date = Date(timeIntervalSince1970: 1_756_800_000)) {
        currentTime = now
    }

    public var now: Date { lock.withLock { currentTime } }

    /// How many sleepers are currently parked.
    public var sleeperCount: Int {
        lock.withLock {
            states.values.filter { if case .parked = $0 { return true } else { return false } }.count
        }
    }

    public func sleep(for duration: Duration) async throws {
        let (id, deadline): (Int, Date) = lock.withLock {
            let id = nextID
            nextID += 1
            states[id] = .pending
            return (id, currentTime.addingTimeInterval(duration.timeInterval))
        }

        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
                enum Outcome { case park, wake, cancel }
                let outcome: Outcome = lock.withLock {
                    if case .cancelled = states[id] {
                        states[id] = nil
                        return .cancel
                    }
                    guard currentTime < deadline else {
                        states[id] = nil
                        return .wake
                    }
                    states[id] = .parked(deadline: deadline, continuation: continuation)
                    return .park
                }
                // Resume outside the lock: the waiting task may run immediately.
                switch outcome {
                case .park: break
                case .wake: continuation.resume()
                case .cancel: continuation.resume(throwing: CancellationError())
                }
            }
        } onCancel: {
            let continuation: CheckedContinuation<Void, any Error>? = lock.withLock {
                switch states[id] {
                case .pending:
                    states[id] = .cancelled
                    return nil
                case .parked(_, let continuation):
                    states[id] = nil
                    return continuation
                case .cancelled, nil:
                    return nil
                }
            }
            continuation?.resume(throwing: CancellationError())
        }
    }

    /// Moves `now` forward and wakes every sleeper whose deadline has passed.
    public func advance(by duration: Duration) {
        let due: [CheckedContinuation<Void, any Error>] = lock.withLock {
            currentTime = currentTime.addingTimeInterval(duration.timeInterval)
            var woken: [CheckedContinuation<Void, any Error>] = []
            for id in Array(states.keys) {
                guard case .parked(let deadline, let continuation) = states[id], deadline <= currentTime else { continue }
                states[id] = nil
                woken.append(continuation)
            }
            return woken
        }
        for continuation in due { continuation.resume() }
    }

    /// Waits — in real time, not clock time — until at least `n` sleepers are
    /// parked. Returns false if `timeout` elapses first.
    public func waitForSleepers(atLeast n: Int, timeout: Duration = .seconds(2)) async -> Bool {
        let deadline = Date().addingTimeInterval(timeout.timeInterval)
        while sleeperCount < n {
            guard Date() < deadline else { return false }
            try? await Task.sleep(for: .milliseconds(1))
        }
        return true
    }
}

extension Duration {
    /// The duration in seconds, for arithmetic against `Date`.
    var timeInterval: TimeInterval {
        let (seconds, attoseconds) = components
        return TimeInterval(seconds) + TimeInterval(attoseconds) / 1e18
    }
}
