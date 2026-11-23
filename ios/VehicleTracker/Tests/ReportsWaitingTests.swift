import Foundation
import Testing
@testable import VehicleTracker

/// Stands in for the wait until the oldest report counted grows too old to
/// upload: it records each wait, and `fire()` ends the ones still set. One
/// that is cancelled throws, as `Task.sleep` does.
@MainActor final class ManualAlarm {
    private(set) var asked: [Duration] = []
    private var waits: [Int: CheckedContinuation<Void, any Error>] = [:]

    /// How many waits are set: neither fired nor cancelled.
    var pending: Int { waits.count }

    func sleep(for duration: Duration) async throws {
        asked.append(duration)
        let id = asked.count
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { waits[id] = $0 }
        } onCancel: {
            Task { @MainActor in self.waits.removeValue(forKey: id)?.resume(throwing: CancellationError()) }
        }
    }

    func fire() {
        let due = waits
        waits = [:]
        for wait in due.values { wait.resume() }
    }
}

/// How many kept reports the trip card says wait on the phone: counted from
/// the queue, the ones too old to upload left out.
@Suite struct ReportsWaitingTests {
    let api = FakeTrackerAPI()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let expiry = ManualAlarm()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "ReportsWaitingTests-\(UUID().uuidString)"

    /// Signed in as ana@example.org to https://transit.example.org.
    func uploader() throws -> (HistoryUploader, OfflineFixQueue, AppSettings) {
        let settings = AppSettings.signedIn(try #require(UserDefaults(suiteName: defaultsName)))
        let queue = OfflineFixQueue(directory: directory)
        let history = HistoryUploader(queue: queue, settings: settings, now: { clock.now },
                                      sleepUntilExpiry: expiry.sleep(for:))
        return (history, queue, settings)
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    /// A report taken `ago` seconds before the clock's now.
    func report(_ ago: Int64) -> LocationReport {
        LocationReport(vehicleID: "4317", tripID: "T1", latitude: 47.6, longitude: -122.33,
                       bearing: 90, speed: 8, accuracy: 5, timestamp: Int64(clock.now.timeIntervalSince1970) - ago)
    }

    var ana: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ana@example.org")! }
    var ben: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ben@example.org")! }

    @Test func eachReportKeptCountsAndAnUploadTakesOffWhatItSent() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        #expect(history.waiting == ReportsWaiting(count: 0, kept: 0))

        await history.keep(report(20))
        #expect(history.waiting == ReportsWaiting(count: 1, kept: 1))
        await history.keep(report(10))
        #expect(history.waiting == ReportsWaiting(count: 2, kept: 2))

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(history.waiting == ReportsWaiting(count: 0, kept: 0))
    }

    /// The server takes 500 an upload, so 100 of 600 are still waiting.
    @Test func anUploadTakesOffOneBatch() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        for ago in (1...600).reversed() {
            try await queue.add(report(Int64(ago)), for: ana)
        }
        await history.countWaiting()
        #expect(history.waiting.count == 600)

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(history.waiting == ReportsWaiting(count: 100, kept: 100))
    }

    @Test func aBatchKeptForNowStaysCounted() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        await history.keep(report(20))
        await history.keep(report(10))
        api.historyError = APIError.transport("offline")

        #expect(await history.uploadIfIdle(through: api) == .kept)
        #expect(history.waiting == ReportsWaiting(count: 2, kept: 2))
    }

    /// The upload drops a report too old for the server rather than send it,
    /// so the count leaves it out from the start: they agree, at the same
    /// boundary as the upload's own test.
    @Test func aReportTooOldToUploadIsNotCounted() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await queue.add(report(HistoryUploader.maxAgeSeconds + 1), for: ana)
        try await queue.add(report(HistoryUploader.maxAgeSeconds), for: ana)
        await history.keep(report(10))
        #expect(history.waiting == ReportsWaiting(count: 2, kept: 3), "exactly the limit still goes, a second older doesn't")

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(api.historyUploads.map(\.points.count) == [2], "what the count said")
        #expect(history.waiting == ReportsWaiting(count: 0, kept: 0))
    }

    /// An upload that finds only reports too old drops them and sends
    /// nothing: none were counted, and now none are kept either.
    @Test func reportsTooOldLeaveTheKeptCountAsTheUploadDropsThem() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await queue.add(report(HistoryUploader.maxAgeSeconds + 20), for: ana)
        try await queue.add(report(HistoryUploader.maxAgeSeconds + 10), for: ana)
        await history.countWaiting()
        #expect(history.waiting == ReportsWaiting(count: 0, kept: 2))

        #expect(await history.uploadIfIdle(through: api) == .nothingQueued)
        #expect(history.waiting == ReportsWaiting(count: 0, kept: 0))
    }

    /// A report ages out with no keep or upload to count it out. The count
    /// is taken again the second the oldest grows too old, and then waits for
    /// the next oldest; a keep that leaves the oldest as it was sets no new
    /// wait.
    @Test func theCountDropsAsTheOldestGrowTooOld() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        await history.keep(report(HistoryUploader.maxAgeSeconds - 100))
        await history.keep(report(HistoryUploader.maxAgeSeconds - 300))
        await history.keep(report(10))
        #expect(history.waiting.count == 3)
        #expect(await eventually { expiry.pending == 1 })
        #expect(expiry.asked == [.seconds(101)], "set once, for the oldest")

        clock.advance(100)
        expiry.fire()
        #expect(await eventually { expiry.asked.count == 2 })
        #expect(history.waiting.count == 3, "100 s on, the oldest still goes")
        #expect(expiry.asked.last == .seconds(1))

        clock.advance(1)
        expiry.fire()
        #expect(await eventually { history.waiting.count == 2 })
        #expect(history.waiting.kept == 3, "still on the phone, until an upload drops it")
        #expect(await eventually { expiry.asked.count == 3 })
        #expect(expiry.asked == [.seconds(101), .seconds(1), .seconds(200)], "then the next oldest")
        #expect(expiry.pending == 1)
    }

    /// With nothing left to count, no wait stays set.
    @Test func nothingLeftToCountSetsNoWait() async throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        await history.keep(report(10))
        #expect(await eventually { expiry.pending == 1 })

        #expect(await history.uploadIfIdle(through: api) == .batchSent)
        #expect(await eventually { expiry.pending == 0 })
        #expect(expiry.asked.count == 1)
    }

    /// Only the driver signed in: another driver's reports are not theirs,
    /// and with nobody signed in there is nothing to count.
    @Test func onlyTheDriverSignedInIsCounted() async throws {
        defer { cleanUp() }
        let (history, queue, settings) = try uploader()
        try await queue.add(report(40), for: ben)
        try await queue.add(report(30), for: ben)
        await history.keep(report(20))
        #expect(history.waiting.count == 1, "ben's are not ana's")

        settings.email = "ben@example.org"
        await history.countWaiting()
        #expect(history.waiting.count == 2)

        settings.email = ""
        await history.countWaiting()
        #expect(history.waiting == ReportsWaiting(count: 0, kept: 0))
    }

    /// Android's `SpeaksWaitingTest`: read out as the line comes and as
    /// reports leave the phone, never as one is kept or ages out.
    @Test(arguments: [
        (ReportsWaiting(count: 0, kept: 0), ReportsWaiting(count: 1, kept: 1), true),
        (ReportsWaiting(count: 0, kept: 2), ReportsWaiting(count: 1, kept: 3), true),
        (ReportsWaiting(count: 1253, kept: 1253), ReportsWaiting(count: 753, kept: 753), true),
        (ReportsWaiting(count: 5, kept: 5), ReportsWaiting(count: 6, kept: 6), false),
        (ReportsWaiting(count: 8640, kept: 8640), ReportsWaiting(count: 8640, kept: 8640), false),
        (ReportsWaiting(count: 5, kept: 6), ReportsWaiting(count: 4, kept: 6), false),
        (ReportsWaiting(count: 5, kept: 5), ReportsWaiting(count: 0, kept: 0), false),
    ])
    func theCountIsReadOutOnlyAsItComesAndAsReportsLeave(old: ReportsWaiting, new: ReportsWaiting, announced: Bool) {
        #expect(new.isAnnounced(after: old) == announced)
    }
}

/// The count through a real `TripSession`, as the trip card shows it.
@Suite struct ReportsWaitingSessionTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let background = FakeBackgroundTime()
    let interval = ManualSleep()
    let expiry = ManualAlarm()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "ReportsWaitingSessionTests-\(UUID().uuidString)"
    let server = URL(string: "https://positions.example.org")!

    var driver: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: server.absoluteString, email: "d@test.com")! }

    func launch() throws -> TripSession {
        let settings = AppSettings(defaults: try #require(UserDefaults(suiteName: defaultsName)))
        let history = HistoryUploader(queue: OfflineFixQueue(directory: directory), settings: settings, now: { clock.now },
                                      backgroundTime: background, sleep: interval.sleep(for:),
                                      sleepUntilExpiry: expiry.sleep(for:))
        return TripSession(settings: settings, tokens: tokens, store: store, locations: locations,
                           access: FakeLocationAccess(), history: history, makeAPI: { _, _ in api }, now: { clock.now })
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    /// Reports kept by an earlier launch, as the files hold them.
    func kept(_ count: Int, for owner: OfflineFixQueue.Owner) async throws {
        let queue = OfflineFixQueue(directory: directory)
        for ago in (1...count).reversed() {
            try await queue.add(LocationReport(vehicleID: "4317", tripID: "T1", latitude: 47.6, longitude: -122.33,
                                               bearing: 90, speed: 8, accuracy: 5,
                                               timestamp: Int64(clock.now.timeIntervalSince1970) - Int64(ago) * 5),
                               for: owner)
        }
    }

    /// The card as `StatusArea` builds it.
    func card(_ session: TripSession) throws -> TripStatus {
        try #require(TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting,
                                reportsWaiting: session.reportsWaiting.count))
    }

    /// A fix `seconds` after 08:05, once the reporter's five seconds are up.
    func fix(_ seconds: TimeInterval) {
        clock.advance(5)
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5).addingTimeInterval(seconds))
    }

    /// The line comes with the first report kept and counts up while there
    /// is no connection. The first report that gets through takes them with
    /// it, and the card says the time and distance again.
    @Test func theTripCardSaysHowManyWaitUntilTheyAreSent() async throws {
        defer { cleanUp() }
        let session = try launch()
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try #require(await eventually { background.running == 0 })
        try await session.start(vehicle: Vehicle(id: "4317", label: ""), tripID: "T1")
        fix(0)
        try #require(await eventually { session.fixesSent == 1 })
        let line2 = try card(session).card.line2
        #expect(try card(session).reportsWaiting == nil)

        api.postError = APIError.transport("offline")
        fix(5)
        #expect(await eventually { session.reportsWaiting.count == 1 })
        #expect(try card(session).card.line2 == "1 report to send when online")
        fix(10)
        #expect(await eventually { session.reportsWaiting.count == 2 })
        #expect(try card(session).card.line2 == "2 reports to send when online")

        api.postError = nil
        fix(15)
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        #expect(await eventually { session.reportsWaiting == ReportsWaiting(count: 0, kept: 0) })
        #expect(try card(session).reportsWaiting == nil)
        #expect(try card(session).card.line2 == line2)
    }

    /// A launch that finds the driver signed in counts what earlier ones
    /// kept as its drain starts, though the upload fails and leaves them.
    @Test func aLaunchCountsWhatEarlierOnesKept() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        let defaults = try #require(UserDefaults(suiteName: defaultsName))
        defaults.set(server.absoluteString, forKey: "serverURL")
        defaults.set("d@test.com", forKey: "email")
        let session = try launch()
        try #require(session.phase == .idle)
        #expect(session.reportsWaiting == ReportsWaiting(count: 0, kept: 0), "not counted yet")
        api.historyError = APIError.transport("offline")

        session.sendKeptReports()
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.count == 1)
        #expect(session.reportsWaiting == ReportsWaiting(count: 2, kept: 2))
    }

    /// Nothing is counted for nobody. A sign-in counts the driver's own,
    /// before any upload has tried them; a sign-out counts nobody's again.
    @Test func aSignInCountsTheDriversOwnAndASignOutNobodys() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()
        #expect(session.reportsWaiting == ReportsWaiting(count: 0, kept: 0))
        api.historyError = APIError.transport("offline")

        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(session.reportsWaiting == ReportsWaiting(count: 2, kept: 2))
        #expect(api.historyUploads.isEmpty, "counted at the sign-in, not by its upload")
        try #require(await eventually { background.running == 0 })

        await session.signOut()
        #expect(session.reportsWaiting == ReportsWaiting(count: 0, kept: 0))
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(session.reportsWaiting.count == 2, "kept for the same driver")
        #expect(await eventually { background.running == 0 })
    }

    /// Another driver's sign-in drops the last one's, so none are counted,
    /// even once that driver signs back in.
    @Test func anotherDriversSignInCountsNone() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()
        api.historyError = APIError.transport("offline")
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try #require(session.reportsWaiting.count == 2)
        try #require(await eventually { background.running == 0 })
        await session.signOut()

        try await session.signIn(serverURL: server, email: "e@test.com", password: "pw")
        #expect(session.reportsWaiting.count == 0)
        try #require(await eventually { background.running == 0 })
        await session.signOut()
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(session.reportsWaiting.count == 0)
        #expect(await eventually { background.running == 0 })
    }
}
