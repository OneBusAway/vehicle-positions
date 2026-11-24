import Foundation
import Testing
import VehiclePositionsKit
@testable import VehicleTracker

@Suite struct HistoryUploaderTests {
    let api = FakeTrackerAPI()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "HistoryUploaderTests-\(UUID().uuidString)"

    /// Signed in as ana@example.org to https://transit.example.org.
    func uploader() throws -> (HistoryUploader, OfflineFixQueue, AppSettings) {
        let settings = AppSettings.signedIn(try #require(UserDefaults(suiteName: defaultsName)))
        let queue = OfflineFixQueue(directory: directory)
        let history = HistoryUploader(queue: queue, settings: settings, now: { clock.now },
                                      backgroundTasks: FakeBackgroundTaskScheduler())
        return (history, queue, settings)
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    /// A report taken `ago` seconds before the clock's now.
    func report(_ ago: Int64, vehicle: String = "bus-1") -> LocationReport {
        LocationReport(vehicleID: vehicle, tripID: "T1", latitude: 47.6, longitude: -122.33,
                       bearing: 90, speed: 8, accuracy: 5, timestamp: Int64(clock.now.timeIntervalSince1970) - ago)
    }

    var ana: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ana@example.org")! }

    @Test func keptReportsGoAsOneUploadForTheirVehicle() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        var withOccupancy = report(20)
        withOccupancy.occupancyStatus = .full
        await history.keep(withOccupancy)
        await history.keep(report(10))

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        try #require(api.historyUploads.count == 1)
        #expect(api.historyUploads[0] == LocationHistoryRequest(vehicleID: "bus-1", reports: [withOccupancy, report(10)]))
        #expect(try await queue.oldest(10, for: ana).isEmpty, "the server took them, so they are gone")
        #expect(await history.uploadIfIdle(through: api) == .nothingQueued)
        #expect(api.historyUploads.count == 1)
    }

    /// The server takes at most 500 reports an upload.
    @Test func anUploadCarriesTheOldest500() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        for ago in (1...600).reversed() {
            try await queue.add(report(Int64(ago)), for: ana)
        }

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        try #require(api.historyUploads.count == 2)
        #expect(api.historyUploads[0].points.map(\.timestamp) == (101...600).reversed().map { report(Int64($0)).timestamp })
        #expect(api.historyUploads[1].points.count == 100)
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    /// An upload names one vehicle, so a driver who changed vehicles between
    /// two dead zones sends each vehicle's reports apart.
    @Test func anUploadStopsWhereTheVehicleChanges() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        await history.keep(report(30))
        await history.keep(report(20))
        await history.keep(report(10, vehicle: "bus-2"))

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(api.historyUploads.map(\.vehicleID) == ["bus-1", "bus-2"])
        #expect(api.historyUploads.map(\.points.count) == [2, 1])
    }

    /// The connection drops again between two batches: the one that went is
    /// gone, the one that didn't is kept whole and goes next time.
    @Test func aBatchThatFailsIsKeptAndTheOnesBeforeItStayGone() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        for ago in (1...600).reversed() {
            try await queue.add(report(Int64(ago)), for: ana)
        }
        #expect(await history.uploadIfIdle(through: api) == .batchSent)

        api.historyError = APIError.transport("offline")
        #expect(await history.uploadIfIdle(through: api) == .kept)
        #expect(try await queue.oldest(.max, for: ana).count == 100)

        api.historyError = nil
        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        try #require(api.historyUploads.count == 3)
        #expect(api.historyUploads[2] == api.historyUploads[1], "the same 100 again")
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    /// Bad data, a vehicle no longer assigned and a server without the
    /// endpoint would be refused however often they were sent; anything else
    /// fails for now. A 401 here has already been through a refresh.
    @Test(arguments: [(400, true), (403, true), (404, true), (401, false), (429, false), (500, false), (503, false)])
    func aBatchRefusedForGoodIsDroppedAndOneThatFailedForNowKept(status: Int, dropped: Bool) async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        api.historyError = APIError.status(status, message: "refused")

        #expect(await history.uploadIfIdle(through: api) == (dropped ? .batchSent : .kept))
        #expect(try await queue.oldest(10, for: ana).count == (dropped ? 0 : 1))
    }

    /// The server takes a report up to a day old; the phone gives itself ten
    /// minutes less. One older is dropped, not sent.
    @Test func aReportTooOldToUploadIsDroppedNotSent() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(HistoryUploader.maxAgeSeconds + 1))
        await history.keep(report(HistoryUploader.maxAgeSeconds))

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        try #require(api.historyUploads.count == 1)
        #expect(api.historyUploads[0].points.map(\.timestamp) == [report(HistoryUploader.maxAgeSeconds).timestamp])
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    @Test func onlyTooOldReportsLeaveNothingToSend() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(HistoryUploader.maxAgeSeconds + 1))

        #expect(await history.uploadIfIdle(through: api) == .nothingQueued)
        #expect(api.historyUploads.isEmpty)
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    /// A phone handed on: the next driver's sign-in never sends the reports
    /// kept for the driver before, which would store them as theirs.
    @Test func anotherDriversReportsAreNotSent() async throws {
        defer { cleanUp() }
        let (history, _, settings) = try uploader()
        await history.keep(report(20))
        await history.keep(report(10))

        settings.email = "ben@example.org"
        #expect(await history.uploadIfIdle(through: api) == .nothingQueued)
        settings.email = "ana@example.org"
        settings.serverURLString = "https://other.example.org"
        #expect(await history.uploadIfIdle(through: api) == .nothingQueued)
        #expect(api.historyUploads.isEmpty)

        settings.serverURLString = "https://transit.example.org/"
        #expect(await history.uploadIfIdle(through: api) == .batchSent, "the same server, retyped")
        #expect(api.historyUploads.map(\.points.count) == [2])
    }

    @Test func nothingIsKeptOrSentWithNoDriverSignedIn() async throws {
        defer { cleanUp() }
        let (history, queue, settings) = try uploader()
        settings.email = ""
        await history.keep(report(10))
        #expect(await history.uploadIfIdle(through: api) == .notSignedIn)

        settings.email = "ana@example.org"
        #expect(try await queue.oldest(10, for: ana).isEmpty, "kept for nobody")
    }

    /// A batch stays at the front of the queue until it has gone, so a second
    /// upload started meanwhile would send it again.
    @Test func oneUploadAtATime() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        await history.keep(report(10))
        let held = ManualSleep()
        // Only the first upload is held, so a second that should never have
        // been sent fails the test rather than hanging it.
        api.whileUploadingHistory = { if held.asked.isEmpty { try await held.sleep(for: .zero) } }

        let first = Task { await history.uploadIfIdle(through: api) }
        #expect(await eventually { held.asked.count == 1 })
        #expect(await history.uploadIfIdle(through: api) == nil)
        #expect(held.asked.count == 1, "the second asked the server nothing")

        held.fire()
        #expect(await first.value == .batchSent)
        #expect(api.historyUploads.count == 1)
    }
}

/// The session hands its uploader to every trip's reporter.
@Suite struct TripSessionHistoryTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "TripSessionHistoryTests-\(UUID().uuidString)"

    @Test func aTripKeepsWhatItCannotSendAndUploadsItOnceAReportGetsThrough() async throws {
        defer {
            try? FileManager.default.removeItem(at: directory)
            UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
        }
        let settings = AppSettings(defaults: try #require(UserDefaults(suiteName: defaultsName)))
        let queue = OfflineFixQueue(directory: directory)
        let session = TripSession(settings: settings, tokens: InMemoryTokenStore(), store: InMemoryActiveTripStore(),
                                  locations: locations, access: FakeLocationAccess(),
                                  history: HistoryUploader(queue: queue, settings: settings, now: { clock.now },
                                                           backgroundTasks: FakeBackgroundTaskScheduler()),
                                  makeAPI: { _, _ in api }, now: { clock.now })
        try await session.signIn(serverURL: URL(string: "https://positions.example.org")!, email: "d@test.com", password: "pw")
        try await session.start(vehicle: Vehicle(id: "bus-1", label: "Bus 1"), tripID: "T1")
        let owner = try #require(OfflineFixQueue.Owner(server: "https://positions.example.org", email: "d@test.com"))

        api.postError = APIError.transport("offline")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { session.reporting == .noNetwork })
        #expect(try await queue.oldest(10, for: owner).map(\.timestamp) == [Int64(TripFixtures.at(8, 5).timeIntervalSince1970)])

        api.postError = nil
        clock.advance(5)
        locations.emitFix(lat: 47.6046, lon: -122.3300, at: TripFixtures.at(8, 5, 5))
        #expect(await eventually { api.historyUploads.count == 1 })
        #expect(api.historyUploads.first?.points.map(\.timestamp) == [Int64(TripFixtures.at(8, 5).timeIntervalSince1970)])
        #expect(session.reporting == .connected)
    }
}

/// A drain: the backlog sent batch after batch with no trip running.
@Suite struct BacklogDrainTests {
    let api = FakeTrackerAPI()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let background = FakeBackgroundTime()
    /// The wait between two batches, fired by hand.
    let interval = ManualSleep()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "BacklogDrainTests-\(UUID().uuidString)"

    /// Signed in as ana@example.org to https://transit.example.org.
    func uploader() throws -> (HistoryUploader, OfflineFixQueue, AppSettings) {
        let settings = AppSettings.signedIn(try #require(UserDefaults(suiteName: defaultsName)))
        let queue = OfflineFixQueue(directory: directory)
        let history = HistoryUploader(queue: queue, settings: settings, now: { clock.now },
                                      backgroundTime: background, backgroundTasks: FakeBackgroundTaskScheduler(),
                                      sleep: interval.sleep(for:))
        return (history, queue, settings)
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    func report(_ ago: Int64) -> LocationReport {
        LocationReport(vehicleID: "bus-1", tripID: "T1", latitude: 47.6, longitude: -122.33,
                       bearing: 90, speed: 8, accuracy: 5, timestamp: Int64(clock.now.timeIntervalSince1970) - ago)
    }

    var ana: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ana@example.org")! }
    var ben: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ben@example.org")! }

    func keep(_ count: Int, for owner: OfflineFixQueue.Owner, in queue: OfflineFixQueue) async throws {
        for ago in (1...count).reversed() {
            try await queue.add(report(Int64(ago)), for: owner)
        }
    }

    /// The server takes one upload every five seconds, so the drain waits
    /// that long between batches, and stops once nothing is left.
    @Test func aDrainSendsBatchAfterBatchFiveSecondsApartUntilNothingIsLeft() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keep(600, for: ana, in: queue)

        history.drain { api }
        #expect(await eventually { interval.asked.count == 1 })
        #expect(api.historyUploads.map(\.points.count) == [500], "the second batch waits")
        #expect(interval.asked == [HistoryUploader.drainInterval])
        #expect(HistoryUploader.drainInterval == .seconds(5))

        interval.fire()
        #expect(await eventually { interval.asked.count == 2 })
        #expect(api.historyUploads.map(\.points.count) == [500, 100])
        #expect(background.running == 1, "still draining")

        interval.fire()
        #expect(await eventually { background.running == 0 })
        #expect(background.begun.count == 1)
        #expect(api.historyUploads.count == 2)
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    /// No connection yet: the batch stays, and the drain stops rather than
    /// try again every five seconds. The next one sends it.
    @Test func aDrainStopsAtABatchThatFailsForNowAndTheNextSendsIt() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keep(2, for: ana, in: queue)
        api.historyError = APIError.transport("offline")

        history.drain { api }
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.count == 1)
        #expect(interval.asked.isEmpty)
        #expect(try await queue.oldest(10, for: ana).count == 2)

        api.historyError = nil
        history.drain { api }
        #expect(await eventually { interval.asked.count == 1 })
        #expect(api.historyUploads.count == 2)
        interval.fire()
        #expect(await eventually { background.running == 0 })
        #expect(background.begun.count == 2)
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    @Test func aDrainSendsNothingWithNobodySignedIn() async throws {
        defer { cleanUp() }
        let (history, queue, settings) = try uploader()
        try await keep(2, for: ana, in: queue)
        settings.email = ""

        history.drain { api }
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.isEmpty)
        #expect(interval.asked.isEmpty)
    }

    /// Each batch goes through what the caller hands over then: none, as
    /// when the driver has signed out or a trip has started, stops it.
    @Test func aDrainStopsOnceItIsHandedNoServer() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keep(600, for: ana, in: queue)
        let handed = Handed(api)

        history.drain { handed.api }
        #expect(await eventually { interval.asked.count == 1 })
        handed.api = nil
        interval.fire()
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.count == 1)
        #expect(try await queue.oldest(.max, for: ana).count == 100)
    }

    @Test func oneDrainAtATimeAndANewOneOnceItIsOver() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keep(2, for: ana, in: queue)

        history.drain { api }
        #expect(await eventually { interval.asked.count == 1 })
        history.drain { api }
        #expect(background.begun.count == 1, "left to the drain that runs")
        interval.fire()
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.count == 1)

        try await keep(1, for: ana, in: queue)
        history.drain { api }
        #expect(await eventually { api.historyUploads.count == 2 })
        #expect(background.begun.count == 2)
        interval.fire()
        #expect(await eventually { background.running == 0 })
    }

    /// A batch from a trip's last report may still be out when the drain
    /// starts: the drain tries again after the interval rather than giving
    /// up or sending that batch a second time.
    @Test func aDrainWaitsForAnUploadAlreadyOut() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        await history.keep(report(20))
        let held = ManualSleep()
        api.whileUploadingHistory = { if held.asked.isEmpty { try await held.sleep(for: .zero) } }
        let tripsUpload = Task { await history.uploadIfIdle(through: api) }
        #expect(await eventually { held.asked.count == 1 })
        await history.keep(report(10))

        history.drain { api }
        #expect(await eventually { interval.asked.count == 1 })
        #expect(api.historyUploads.isEmpty, "nothing sent beside the batch out")

        held.fire()
        #expect(await tripsUpload.value == .batchSent)
        interval.fire()
        #expect(await eventually { interval.asked.count == 2 })
        #expect(api.historyUploads.map(\.points.count) == [1, 1])
        #expect(api.historyUploads.map(\.points.first?.timestamp) == [report(20).timestamp, report(10).timestamp])
        interval.fire()
        #expect(await eventually { background.running == 0 })
    }

    /// iOS takes the background time back with a batch out: the drain stops,
    /// the batch stays for the next one, and the time is ended once, in
    /// time.
    @Test func runningOutOfBackgroundTimeStopsTheDrainAndKeepsTheBatchOut() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keep(2, for: ana, in: queue)
        let held = ManualSleep()
        api.whileUploadingHistory = { if held.asked.isEmpty { try await held.sleep(for: .zero) } }

        history.drain { api }
        #expect(await eventually { held.asked.count == 1 })
        background.expire()
        #expect(background.running == 0, "ended before the expiry returns")
        #expect(await eventually { interval.asked.isEmpty && api.historyUploads.isEmpty && background.ended.count == 1 })
        #expect(try await queue.oldest(10, for: ana).count == 2)

        // The drain has stopped, so the next can start.
        history.drain { api }
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        interval.fire()
        #expect(await eventually { background.running == 0 })
        #expect(background.ended.map(\.rawValue) == [1, 2], "each time ended once")
    }

    @Test func dropsOnlyTheReportsKeptForOtherSignIns() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keep(2, for: ana, in: queue)
        try await keep(3, for: ben, in: queue)

        await history.dropReportsKeptForOthers()
        #expect(try await queue.oldest(10, for: ana).count == 2)
        #expect(try await queue.oldest(10, for: ben).isEmpty)
        #expect(try await OfflineFixQueue(directory: directory).oldest(10, for: ben).isEmpty, "and off disk")
    }

    @Test func withNobodySignedInNothingIsDropped() async throws {
        defer { cleanUp() }
        let (history, queue, settings) = try uploader()
        try await keep(2, for: ben, in: queue)
        settings.email = ""

        await history.dropReportsKeptForOthers()
        #expect(try await OfflineFixQueue(directory: directory).oldest(10, for: ben).count == 2)
    }
}

/// What a drain is handed for each batch, changed by a test while it runs.
@MainActor private final class Handed {
    var api: (any TrackerAPI)?
    init(_ api: any TrackerAPI) { self.api = api }
}
