import Foundation
import Testing
import VehiclePositionsKit
@testable import VehicleTracker

/// What the reporter does with a report while the server is down behind the
/// proxy in front of it (docs/deployment.md, section 7). The phone still
/// reaches the proxy, which answers for the server with a 502, 503 or 504,
/// so no report fails for want of a connection.
@Suite struct ServerDownReportsTests {
    let api = FakeTrackerAPI()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "ServerDownReportsTests-\(UUID().uuidString)"
    var ana: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ana@example.org")! }

    /// A reporter whose uploader keeps reports in `directory`, for
    /// ana@example.org on https://transit.example.org.
    func reporter() throws -> (LocationReporter, OfflineFixQueue) {
        let settings = AppSettings.signedIn(try #require(UserDefaults(suiteName: defaultsName)))
        let queue = OfflineFixQueue(directory: directory)
        let history = HistoryUploader(queue: queue, settings: settings, now: { clock.now },
                                      backgroundTasks: FakeBackgroundTaskScheduler())
        return (LocationReporter(api: api, now: { clock.now }, history: history), queue)
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    /// Sends a fix taken now, waits for it and its batch, then moves on five
    /// seconds, so the next one is due.
    func send(_ r: LocationReporter) async {
        let fix = LocationFix(latitude: 47.6, longitude: -122.33, horizontalAccuracy: 5, speed: 8, course: 90,
                              timestamp: clock.now)
        #expect(r.report(fix, vehicleID: "bus-1", gtfsTripID: "T1", routeID: "R1", startDate: "20260902"))
        await r.waitForInFlightSend()
        clock.advance(5)
    }

    /// The server never answers these itself on this endpoint; the proxy
    /// does, with a page of its own, when the server isn't listening (a
    /// restart, a crash) or doesn't answer in time.
    @Test(arguments: [502, 503, 504])
    func aReportTheProxyAnswersForIsKeptAndSentOnceTheServerIsBack(status: Int) async throws {
        defer { cleanUp() }
        let (r, queue) = try reporter()
        api.postError = APIError.status(status, message: "")
        await send(r)
        await send(r)
        let kept = api.posted

        #expect(r.problem == .serverError, "the line under the card says what it said before")
        #expect(r.fixesSent == 0)
        #expect(try await queue.oldest(10, for: ana) == kept, "each kept as it was posted, in order")

        api.postError = nil
        await send(r)
        #expect(api.historyUploads == [LocationHistoryRequest(vehicleID: "bus-1", reports: kept)])
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }
}
