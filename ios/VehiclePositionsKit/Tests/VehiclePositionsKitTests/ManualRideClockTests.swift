import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct ManualRideClockTests {
    @Test func advanceResumesSleepersInOrder() async throws {
        let clock = ManualRideClock()
        let start = clock.now
        let task = Task { try await clock.sleep(for: .seconds(5)); return clock.now }
        #expect(await clock.waitForSleepers(atLeast: 1))
        clock.advance(by: .seconds(2))
        #expect(clock.sleeperCount == 1)
        clock.advance(by: .seconds(3))
        let woke = try await task.value
        #expect(woke == start.addingTimeInterval(5))
        #expect(clock.sleeperCount == 0)
    }

    @Test @MainActor func advanceWakesSleepersInDeadlineOrder() async throws {
        let clock = ManualRideClock()
        let log = WakeLog()
        // The longer sleep parks first, so a correct wake order can only come
        // from the deadlines, never from the order the sleepers registered in.
        let long = Task { @MainActor in
            try await clock.sleep(for: .seconds(10))
            log.record("long")
        }
        #expect(await clock.waitForSleepers(atLeast: 1))
        let short = Task { @MainActor in
            try await clock.sleep(for: .seconds(1))
            log.record("short")
        }
        #expect(await clock.waitForSleepers(atLeast: 2))

        clock.advance(by: .seconds(10))
        try await long.value
        try await short.value
        #expect(log.entries == ["short", "long"])
    }

    @Test func cancellationThrows() async {
        let clock = ManualRideClock()
        let task = Task { try await clock.sleep(for: .seconds(60)) }
        _ = await clock.waitForSleepers(atLeast: 1)
        task.cancel()
        await #expect(throws: CancellationError.self) { try await task.value }
    }
}

/// Records wake order on the main actor, where jobs run in the order they were
/// enqueued — so the log reflects the clock's resume order, not the scheduler's.
@MainActor
private final class WakeLog {
    private(set) var entries: [String] = []

    func record(_ name: String) {
        entries.append(name)
    }
}
