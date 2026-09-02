import Foundation
@testable import VehiclePositionsKit

actor EventCollector {
    private(set) var events: [RideEvent] = []
    init(_ stream: AsyncStream<RideEvent>) {
        // Not stored: a nonisolated actor init may not write a property after
        // `self` has escaped into the closure, and the drain task needs no
        // cancelling — every ride finishes its stream.
        Task { [weak self] in
            for await e in stream { await self?.append(e) }
        }
    }
    private func append(_ e: RideEvent) { events.append(e) }
    /// Polls until `predicate` is satisfied or `timeout` elapses (real time).
    func wait(timeout: Duration = .seconds(5), _ predicate: @Sendable ([RideEvent]) -> Bool) async -> Bool {
        let deadline = ContinuousClock.now + timeout
        while ContinuousClock.now < deadline {
            if predicate(events) { return true }
            try? await Task.sleep(for: .milliseconds(5))
        }
        return predicate(events)
    }
    func waitForEnd(timeout: Duration = .seconds(5)) async -> RideEndReason? {
        _ = await wait(timeout: timeout) { $0.contains { if case .ended = $0 { return true }; return false } }
        for e in events { if case let .ended(r, _) = e { return r } }
        return nil
    }
}
