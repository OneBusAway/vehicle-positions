import Foundation
import Testing
import VehiclePositionsKit
@testable import VehicleTracker

@Suite struct LocationReporterTests {
    let api = FakeTrackerAPI()
    let clock = ManualClock(start: TripFixtures.at(8, 0))

    func reporter() -> LocationReporter { LocationReporter(api: api, now: { clock.now }) }

    func fix(bearing: Double = 90, speed: Double = 8, accuracy: Double = 5) -> LocationFix {
        LocationFix(latitude: 47.6, longitude: -122.33, horizontalAccuracy: accuracy, speed: speed, course: bearing, timestamp: clock.now)
    }

    @Test func sendsTheFixAsAReport() async {
        let r = reporter()
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        await r.waitForInFlightSend()
        #expect(api.posted.count == 1)
        let p = api.posted[0]
        #expect(p.vehicleID == "bus-1")
        #expect(p.tripID == "T1")
        #expect(p.bearing == 90)
        #expect(p.speed == 8)
        #expect(p.accuracy == 5)
        #expect(p.timestamp == Int64(TripFixtures.at(8, 0).timeIntervalSince1970))
        #expect(r.fixesSent == 1)
        #expect(r.problem == .none)
    }

    @Test func dropsUnknownBearingSpeedAndAccuracy() async {
        let r = reporter()
        r.report(fix(bearing: -1, speed: -1, accuracy: -1), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        let p = api.posted[0]
        #expect(p.bearing == nil)
        #expect(p.speed == 0, "an unknown speed is sent as 0, the server's floor")
        #expect(p.accuracy == nil)
    }

    @Test func throttlesToOneReportPerFiveSeconds() async {
        let r = reporter()
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        await r.waitForInFlightSend()
        clock.advance(2)
        #expect(!r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        clock.advance(3)
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        await r.waitForInFlightSend()
        #expect(api.posted.count == 2)
    }

    @Test func mapsServerErrors() async {
        let r = reporter()
        api.postError = APIError.status(401, message: "invalid token")
        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        #expect(r.problem == .authExpired)

        clock.advance(5)
        api.postError = APIError.status(429, message: "rate limit exceeded")
        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        #expect(r.problem == .authExpired, "429 is dropped silently and leaves the status alone")

        clock.advance(5)
        api.postError = APIError.transport("offline")
        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        #expect(r.problem == .noNetwork)

        clock.advance(5)
        api.postError = nil
        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        #expect(r.problem == .none)
        #expect(r.fixesSent == 1)
    }

    @Test func threeTimestampRejectsMeanClockSkew() async {
        let r = reporter()
        api.postError = APIError.status(400, message: "timestamp must be within 5 minutes of server time")
        for _ in 0..<2 {
            r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
            await r.waitForInFlightSend()
            clock.advance(5)
        }
        #expect(r.problem == .none)

        // An interleaved, unrelated server error breaks the run: the two
        // prior timestamp rejects no longer count toward the threshold.
        api.postError = APIError.status(500, message: "internal error")
        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        clock.advance(5)

        api.postError = APIError.status(400, message: "timestamp must be within 5 minutes of server time")
        for _ in 0..<2 {
            r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
            await r.waitForInFlightSend()
            clock.advance(5)
        }
        #expect(r.problem == .none, "only two consecutive timestamp rejects since the 500 broke the run")

        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        #expect(r.problem == .clockSkew)
        clock.advance(5)
        api.postError = nil
        r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1")
        await r.waitForInFlightSend()
        #expect(r.problem == .none)
    }

    @Test func aFailedSendStillCountsForTheThrottle() async {
        let r = reporter()
        api.postError = APIError.transport("offline")
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        await r.waitForInFlightSend()
        #expect(!r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"), "no clock advance: still inside the five-second window even though the first send failed")
        #expect(api.posted.count == 1)
    }

    /// A slow POST must not hold up the fix behind it: `report` hands the
    /// send over and returns, so the caller is never parked on the network.
    @Test func reportDoesNotWaitForTheSend() async {
        let r = reporter()
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        #expect(api.posted.isEmpty, "the send has not run yet, and the caller already has control back")
        await r.waitForInFlightSend()
        #expect(api.posted.count == 1)
    }

    /// While a send is still out, a later fix is dropped rather than queued
    /// behind it — a position that old is no use by the time it would land.
    @Test func aSendInFlightBlocksTheNextReport() async {
        let r = reporter()
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"))
        clock.advance(10)
        #expect(!r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"), "the throttle window has passed, but the first send has not finished")
        await r.waitForInFlightSend()
        #expect(api.posted.count == 1)
        #expect(r.report(fix(), vehicleID: "bus-1", gtfsTripID: "T1"), "once it lands, the next fix goes out")
        await r.waitForInFlightSend()
        #expect(api.posted.count == 2)
    }
}

/// A clock the test moves by hand.
@MainActor final class ManualClock {
    private(set) var now: Date
    init(start: Date) { now = start }
    func advance(_ seconds: TimeInterval) { now = now.addingTimeInterval(seconds) }
}
