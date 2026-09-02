import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct SampleBufferTests {
    func fix(_ t: TimeInterval) -> LocationFix { LocationFix(latitude: 1, longitude: 2, horizontalAccuracy: 5, speed: 1, course: 0, timestamp: Date(timeIntervalSince1970: t)) }

    @Test func takeReturnsOldestFirstAndRemoves() {
        var b = SampleBuffer(retention: .seconds(600))
        let now = Date(timeIntervalSince1970: 1000)
        b.append(fix(990), now: now); b.append(fix(995), now: now); b.append(fix(1000), now: now)
        let taken = b.take(max: 2)
        #expect(taken.map(\.timestamp.timeIntervalSince1970) == [990, 995])
        #expect(b.count == 1)
        b.restore(taken)
        #expect(b.take(max: 10).map(\.timestamp.timeIntervalSince1970) == [990, 995, 1000])
    }

    @Test func pruneDropsOldSamples() {
        var b = SampleBuffer(retention: .seconds(600))
        b.append(fix(100), now: Date(timeIntervalSince1970: 100))
        b.append(fix(800), now: Date(timeIntervalSince1970: 800))
        #expect(b.count == 1)
    }
}
