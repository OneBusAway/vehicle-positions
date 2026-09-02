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

    @Test func cancellationThrows() async {
        let clock = ManualRideClock()
        let task = Task { try await clock.sleep(for: .seconds(60)) }
        _ = await clock.waitForSleepers(atLeast: 1)
        task.cancel()
        await #expect(throws: CancellationError.self) { try await task.value }
    }
}
