import BackgroundTasks
import Foundation
import Testing
import UIKit
import VehiclePositionsKit
@testable import VehicleTracker

/// The background task that sends kept reports for a phone that never comes
/// back to the front: when it is asked for, and what a run does.
@Suite struct KeptReportsTaskTests {
    let api = FakeTrackerAPI()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let background = FakeBackgroundTime()
    let tasks = FakeBackgroundTaskScheduler()
    /// The wait between two batches of a drain, fired by hand.
    let interval = ManualSleep()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "KeptReportsTaskTests-\(UUID().uuidString)"
    let id = HistoryUploader.backgroundTaskIdentifier

    /// Signed in as ana@example.org to https://transit.example.org, with the
    /// task registered as the app registers it, through a drain.
    func uploader() throws -> (HistoryUploader, OfflineFixQueue, AppSettings) {
        let settings = AppSettings.signedIn(try #require(UserDefaults(suiteName: defaultsName)))
        let queue = OfflineFixQueue(directory: directory)
        let history = HistoryUploader(queue: queue, settings: settings, now: { clock.now },
                                      backgroundTime: background, backgroundTasks: tasks, sleep: interval.sleep(for:))
        history.registerBackgroundTask { [weak history, api] in history?.drain { api } }
        return (history, queue, settings)
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    func report(_ ago: Int64) -> LocationReport {
        LocationReport(vehicleID: "4317", tripID: "T1", latitude: 47.6, longitude: -122.33,
                       bearing: 90, speed: 8, accuracy: 5, timestamp: Int64(clock.now.timeIntervalSince1970) - ago)
    }

    var ana: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ana@example.org")! }

    /// Kept by an earlier launch: on disk, with no request asked for by this
    /// one.
    func keptEarlier(_ count: Int, in queue: OfflineFixQueue) async throws {
        for ago in (1...count).reversed() {
            try await queue.add(report(Int64(ago)), for: ana)
        }
    }

    /// Hours after the trip, the access token a1 has long expired, so each
    /// upload waits on a token refresh, which the test answers by hand.
    func uploadsWaitOnARefresh() -> (ScriptedRefresh, ScriptedTokenStore) {
        let signIn = UUID()
        let tokens = ScriptedTokenStore(StoredToken(token: "a1", issuedAt: clock.now.addingTimeInterval(-900),
                                                    refreshToken: "r1", expiresAt: clock.now, signInID: signIn))
        let refresher = TokenRefresher(tokens: tokens)
        let refresh = ScriptedRefresh()
        // URLSession fails a cancelled task's request instead of handing
        // back the server's answer.
        let present: TokenRefresher.Refresh = { refreshToken in
            let reply = await refresh.call(refreshToken)
            return Task.isCancelled ? .unreachable("cancelled") : reply
        }
        // What `TrackerClient` does with a batch the server turned away with
        // a 401: waits on the app's one refresh, then sends the batch again,
        // which URLSession refuses for a cancelled drain.
        api.whileUploadingHistory = {
            _ = await refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: present)
            try Task.checkCancellation()
        }
        return (refresh, tokens)
    }

    // MARK: When it is asked for

    /// The trip may end before the connection comes back, and the phone sit
    /// locked in its holder until the next shift.
    @Test func aReportKeptAsksForARunOnceThereIsAConnection() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()

        await history.keep(report(10))
        try #require(tasks.submitted.count == 1)
        let request = try #require(tasks.submitted[0] as? BGProcessingTaskRequest)
        #expect(request.identifier == id)
        #expect(request.requiresNetworkConnectivity)
        #expect(!request.requiresExternalPower)
        #expect(request.earliestBeginDate == nil)
        #expect(tasks.waiting.keys.sorted() == [id])
        #expect(try await queue.oldest(10, for: ana).count == 1)
    }

    @Test func aDrainThatStopsWithReportsLeftAsksForARun() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keptEarlier(2, in: queue)
        api.historyError = APIError.transport("offline")

        await history.drain { api }.value
        #expect(api.historyUploads.count == 1)
        #expect(tasks.submitted.map(\.identifier) == [id])
        #expect(tasks.waiting.keys.sorted() == [id])
    }

    @Test func aDrainThatSendsEverythingAsksForNone() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        try await keptEarlier(2, in: queue)

        let drain = history.drain { api }
        #expect(await eventually { interval.asked.count == 1 })
        interval.fire()
        await drain.value
        #expect(api.historyUploads.map(\.points.count) == [2])
        #expect(tasks.submitted.isEmpty)
    }

    @Test func aDrainWithNobodySignedInAsksForNone() async throws {
        defer { cleanUp() }
        let (history, queue, settings) = try uploader()
        try await keptEarlier(2, in: queue)
        settings.email = ""

        await history.drain { api }.value
        #expect(api.historyUploads.isEmpty)
        #expect(tasks.submitted.isEmpty)
    }

    /// iOS replaces a request still waiting with the next one for the same
    /// task, so one is enough: a report kept every five seconds asks once,
    /// and the next ask comes once a run has taken the request.
    @Test func requestsDoNotPileUp() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        api.historyError = APIError.transport("offline")

        for ago: Int64 in [30, 25, 20] {
            await history.keep(report(ago))
        }
        #expect(tasks.submitted.count == 1)
        await history.drain { api }.value
        #expect(tasks.submitted.count == 1, "asked already, and still waiting")

        let run = try #require(tasks.run(id))
        #expect(await eventually { run.completions == [false] })
        #expect(tasks.submitted.count == 2, "the run took the request, and reports are left")
        #expect(Set(tasks.submitted.map(\.identifier)) == [id])
        #expect(tasks.waiting.count == 1)
        #expect(try await queue.oldest(10, for: ana).count == 3)
    }

    /// Background App Refresh off, or the simulator: iOS refuses for good,
    /// so the reports are still kept, and the app doesn't ask again for each.
    @Test func aRequestIOSRefusesKeepsTheReportsAndIsNotAskedAgainForEach() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        tasks.submitError = BGTaskScheduler.Error(.unavailable)

        await history.keep(report(10))
        await history.keep(report(5))
        #expect(tasks.submitted.count == 1)
        #expect(tasks.waiting.isEmpty)
        #expect(try await queue.oldest(10, for: ana).count == 2)
        #expect(history.waiting.count == 2)
    }

    // MARK: A run

    @Test func aRunSendsEverythingBatchByBatchAndCompletes() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(200))
        try await keptEarlier(149, in: queue)

        let run = try #require(tasks.run(id))
        #expect(await eventually { interval.asked.count == 1 })
        #expect(api.historyUploads.map(\.points.count) == [80])
        #expect(run.completions.isEmpty, "the second batch waits")
        interval.fire()
        #expect(await eventually { interval.asked.count == 2 })
        interval.fire()
        #expect(await eventually { run.completions == [true] })
        #expect(api.historyUploads.map(\.points.count) == [80, 70])
        #expect(try await queue.oldest(10, for: ana).isEmpty)
        #expect(tasks.submitted.count == 1, "nothing left to ask for")
        #expect(background.running == 0)
    }

    /// The server is still out of reach: the reports stay, and the next run
    /// is asked for before this one completes, as iOS wants.
    @Test func aRunThatCannotSendAsksForTheNextBeforeItCompletes() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        api.historyError = APIError.transport("offline")

        let run = try #require(tasks.run(id))
        #expect(await eventually { run.completions == [false] })
        #expect(tasks.events == ["submit", "submit", "complete(false)"])
        #expect(tasks.waiting.keys.sorted() == [id])
        #expect(try await queue.oldest(10, for: ana).count == 1)
    }

    /// iOS ends the run with a batch out: the drain stops, the batch stays
    /// for the next run, which is asked for, and the run completes in time.
    @Test func iOSEndingARunStopsTheDrainKeepsTheBatchOutAndAsksAgain() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        await history.keep(report(5))
        let held = ManualSleep()
        api.whileUploadingHistory = { if held.asked.isEmpty { try await held.sleep(for: .zero) } }

        let run = try #require(tasks.run(id))
        #expect(await eventually { held.asked.count == 1 })
        run.expire()
        #expect(await eventually { run.completions == [false] })
        #expect(tasks.events == ["submit", "submit", "complete(false)"])
        #expect(api.historyUploads.isEmpty)
        #expect(try await queue.oldest(10, for: ana).count == 2)
        #expect(await eventually { background.running == 0 }, "ended by the drain as it stops")
        #expect(background.ended.count == 1, "the drain's own time, ended once")

        let next = try #require(tasks.run(id))
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        interval.fire()
        #expect(await eventually { next.completions == [true] })
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    /// A run hours after the trip starts with a token refresh, the access
    /// token long expired, and iOS may end the run while the drain waits on
    /// it. The run completes at once, not once the refresh has its answer.
    /// The refresh is left to finish and save the new pair: the server may
    /// already have spent r1, and presenting it again would revoke every
    /// refresh token the driver holds.
    @Test func iOSEndingARunDuringATokenRefreshCompletesItAtOnceAndLetsTheRefreshFinish() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        let (refresh, tokens) = uploadsWaitOnARefresh()

        let run = try #require(tasks.run(id))
        try #require(await eventually { refresh.presented == ["r1"] })
        run.expire()
        #expect(run.completions == [false], "at once, with the refresh still out")
        #expect(tasks.events == ["submit", "submit", "complete(false)"])
        #expect(background.running == 1, "the drain keeps its time until the refresh has its answer")

        refresh.answer(.issued(AuthTokens(accessToken: "a2", refreshToken: "r2", expiresIn: 900)))
        #expect(await eventually { background.running == 0 }, "the drain stopped")
        #expect(try tokens.load()?.refreshToken == "r2", "the refresh saved the new pair")
        #expect(refresh.presented == ["r1"])
        #expect(api.historyUploads.isEmpty)
        #expect(try await queue.oldest(10, for: ana).count == 1)

        api.whileUploadingHistory = nil
        let next = try #require(tasks.run(id))
        #expect(await eventually { api.historyUploads.map(\.points.count) == [1] })
        interval.fire()
        #expect(await eventually { next.completions == [true] })
        #expect(run.completions == [false], "not completed again as its drain stopped")
    }

    /// The drain the app started as the trip ended waits on a token refresh,
    /// and iOS takes its background time back. The refresh doesn't stop for
    /// it (`TokenRefresher` says why), so the stopped drain may still be
    /// there when iOS next wakes the app for a run. That drain would send
    /// nothing, so the run starts its own, which waits for the stopped one's
    /// upload rather than send beside it, and then sends.
    @Test func aRunAfterADrainStoppedDuringATokenRefreshSendsThroughADrainOfItsOwn() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        let (refresh, tokens) = uploadsWaitOnARefresh()
        let stopped = history.drain { api }
        try #require(await eventually { refresh.presented == ["r1"] })
        background.expire()
        try #require(stopped.isCancelled)
        try #require(background.ended.map(\.rawValue) == [1])

        let run = try #require(tasks.run(id))
        #expect(await eventually { interval.asked.count == 1 }, "the run's drain found an upload out, and waits")
        #expect(background.running == 1, "the run's drain has time of its own")
        #expect(api.historyUploads.isEmpty)

        refresh.answer(.issued(AuthTokens(accessToken: "a2", refreshToken: "r2", expiresIn: 900)))
        await stopped.value
        #expect(background.ended.map(\.rawValue) == [1], "the stopped drain left the run's drain its time")
        history.drain { api }
        #expect(background.begun.count == 2, "and left it the running drain")
        interval.fire()
        #expect(await eventually { api.historyUploads.map(\.points.count) == [1] })
        #expect(await eventually { interval.asked.count == 2 })
        interval.fire()
        #expect(await eventually { run.completions == [true] })
        #expect(refresh.presented == ["r1"])
        #expect(try tokens.load()?.refreshToken == "r2")
        #expect(try await queue.oldest(10, for: ana).isEmpty)
        #expect(background.ended.map(\.rawValue) == [1, 2])
    }

    /// iOS ends a run while its drain waits on a token refresh, and the app
    /// comes to the front before the refresh has its answer. The stopped
    /// drain keeps its own background time until then, and the front gets a
    /// drain of its own, which sends once the stopped one's upload is over.
    @Test func aDrainARunStoppedDuringATokenRefreshIsNotHandedToTheFront() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        let (refresh, _) = uploadsWaitOnARefresh()
        let run = try #require(tasks.run(id))
        try #require(await eventually { refresh.presented == ["r1"] })
        run.expire()
        try #require(run.completions == [false])

        let front = history.drain { api }
        #expect(!front.isCancelled)
        #expect(await eventually { interval.asked.count == 1 }, "the front's drain found an upload out, and waits")
        #expect(background.running == 2)

        refresh.answer(.issued(AuthTokens(accessToken: "a2", refreshToken: "r2", expiresIn: 900)))
        #expect(await eventually { background.ended.map(\.rawValue) == [1] }, "the stopped drain ended its own time")
        interval.fire()
        #expect(await eventually { api.historyUploads.map(\.points.count) == [1] })
        #expect(await eventually { interval.asked.count == 2 })
        interval.fire()
        await front.value
        #expect(try await queue.oldest(10, for: ana).isEmpty)
        #expect(background.ended.map(\.rawValue) == [1, 2])
        #expect(run.completions == [false])
    }

    /// iOS may call the expiry just as the run completes, and it lands on
    /// the main actor after, when the app may have come to the front and
    /// started a drain of its own. That drain is not the run's to stop, and
    /// nor is its background time.
    @Test func anExpiryLandingAfterTheRunLeavesALaterDrainAlone() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        let run = try #require(tasks.run(id))
        #expect(await eventually { interval.asked.count == 1 })
        interval.fire()
        try #require(await eventually { run.completions == [true] })

        try await queue.add(report(5), for: ana)
        let held = ManualSleep()
        api.whileUploadingHistory = { if held.asked.isEmpty { try await held.sleep(for: .zero) } }
        let front = history.drain { api }
        try #require(await eventually { held.asked.count == 1 })

        run.expire()
        #expect(!front.isCancelled)
        #expect(background.running == 1, "the front's drain keeps its time")
        #expect(run.completions == [true])
        #expect(tasks.events == ["submit", "complete(true)"])

        held.fire()
        #expect(await eventually { interval.asked.count == 2 })
        interval.fire()
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.map(\.points.count) == [1, 1])
        #expect(try await queue.oldest(10, for: ana).isEmpty)
    }

    /// A drain started as the trip ended may still be going: the run waits
    /// for it rather than start a second.
    @Test func aRunWaitsForTheDrainAlreadyGoing() async throws {
        defer { cleanUp() }
        let (history, queue, _) = try uploader()
        await history.keep(report(10))
        try await keptEarlier(149, in: queue)
        history.drain { api }
        #expect(await eventually { interval.asked.count == 1 })

        let run = try #require(tasks.run(id))
        #expect(background.begun.count == 1, "no second drain")
        interval.fire()
        #expect(await eventually { interval.asked.count == 2 })
        #expect(run.completions.isEmpty)
        interval.fire()
        #expect(await eventually { run.completions == [true] })
        #expect(api.historyUploads.map(\.points.count) == [80, 70])
    }

    @Test func aRunWithNobodySignedInSendsNothingAndCompletes() async throws {
        defer { cleanUp() }
        let (history, _, settings) = try uploader()
        await history.keep(report(10))
        settings.email = ""

        let run = try #require(tasks.run(id))
        #expect(await eventually { run.completions == [true] })
        #expect(api.historyUploads.isEmpty)
        #expect(tasks.submitted.count == 1)
    }

    // MARK: Registering

    @Test func theTaskIsRegisteredOnceUnderTheIdentifierInfoPlistPermits() throws {
        defer { cleanUp() }
        let (history, _, _) = try uploader()
        #expect(history.isBackgroundTaskRegistered)
        #expect(tasks.registered == [id])
    }

    @Test func aTaskInfoPlistDoesNotPermitIsNotRegistered() throws {
        let settings = AppSettings.signedIn(try #require(UserDefaults(suiteName: defaultsName)))
        defer { cleanUp() }
        let refusing = FakeBackgroundTaskScheduler(permitted: [])
        let history = HistoryUploader(queue: OfflineFixQueue(directory: directory), settings: settings,
                                      backgroundTasks: refusing)
        history.registerBackgroundTask { nil }
        #expect(!history.isBackgroundTaskRegistered)
        #expect(refusing.registered.isEmpty)
    }
}

/// Through a real `TripSession`, wired as `AppContainer` wires it.
@Suite struct KeptReportsTaskSessionTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let background = FakeBackgroundTime()
    let tasks = FakeBackgroundTaskScheduler()
    let interval = ManualSleep()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "KeptReportsTaskSessionTests-\(UUID().uuidString)"
    let server = URL(string: "https://positions.example.org")!
    let vehicle = Vehicle(id: "4317", label: "Vehicle 4317")
    let id = HistoryUploader.backgroundTaskIdentifier

    var driver: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: server.absoluteString, email: "d@test.com")! }

    func launch() throws -> TripSession {
        let settings = AppSettings(defaults: try #require(UserDefaults(suiteName: defaultsName)))
        let history = HistoryUploader(queue: OfflineFixQueue(directory: directory), settings: settings, now: { clock.now },
                                      backgroundTime: background, backgroundTasks: tasks, sleep: interval.sleep(for:))
        let session = TripSession(settings: settings, tokens: tokens, store: store, locations: locations,
                                  access: FakeLocationAccess(), history: history, makeAPI: { _, _ in api },
                                  now: { clock.now })
        history.registerBackgroundTask { [weak session] in session?.sendKeptReports() }
        return session
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    func left() async throws -> Int {
        try await OfflineFixQueue(directory: directory).oldest(.max, for: driver).count
    }

    /// Signed in, on a trip with no connection, one report kept.
    func tripWithAReportKept(_ session: TripSession) async throws {
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try #require(await eventually { background.running == 0 })
        try await session.start(vehicle: vehicle, tripID: "T1")
        api.postError = APIError.transport("offline")
        api.historyError = APIError.transport("offline")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        try #require(await eventually { session.reporting == .noNetwork })
        try #require(try await left() == 1)
    }

    /// The trip ends in a dead zone and the phone is never opened again
    /// before the next shift: the run iOS starts once the connection is back
    /// sends what was kept.
    @Test func aTripEndedWithoutAConnectionIsSentByTheRun() async throws {
        defer { cleanUp() }
        let session = try launch()
        try await tripWithAReportKept(session)
        #expect(tasks.submitted.map(\.identifier) == [id])

        session.endLocally()
        #expect(await eventually { background.running == 0 })
        #expect(try await left() == 1, "the end's drain found no connection")
        #expect(tasks.submitted.count == 1, "the request is still waiting")

        api.historyError = nil
        let run = try #require(tasks.run(id))
        #expect(await eventually { api.historyUploads.count == 2 })
        #expect(api.historyUploads.last?.points.map(\.timestamp) == [Int64(TripFixtures.at(8, 5).timeIntervalSince1970)])
        interval.fire()
        #expect(await eventually { run.completions == [true] })
        #expect(try await left() == 0)
        #expect(session.phase == .idle)
    }

    /// During a trip each report that gets through takes a batch, so a run
    /// leaves them to the trip, and asks for the next in case it ends
    /// without a connection.
    @Test func aRunDuringATripLeavesTheReportsToIt() async throws {
        defer { cleanUp() }
        let session = try launch()
        try await tripWithAReportKept(session)
        api.historyError = nil

        let run = try #require(tasks.run(id))
        #expect(await eventually { run.completions == [false] })
        #expect(api.historyUploads.isEmpty)
        #expect(tasks.submitted.count == 2)
        #expect(try await left() == 1)
        if case .active = session.phase {} else { Issue.record("the trip runs on: \(session.phase)") }
    }
}

/// The app as it launched to host these tests.
@Suite struct KeptReportsTaskLaunchTests {
    /// The app these tests run in registered the task with the real
    /// `BGTaskScheduler`, which takes it only if Info.plist permits it, and
    /// treats a registration after launch as an error.
    @Test func theAppRegisteredTheTaskAsItLaunched() {
        #expect(AppContainer.shared.history.isBackgroundTaskRegistered)
    }

    @Test func infoPlistPermitsTheTaskAndBackgroundProcessing() {
        let permitted = Bundle.main.object(forInfoDictionaryKey: "BGTaskSchedulerPermittedIdentifiers") as? [String]
        #expect(permitted == [HistoryUploader.backgroundTaskIdentifier])
        let modes = Bundle.main.object(forInfoDictionaryKey: "UIBackgroundModes") as? [String]
        #expect(modes == ["location", "processing"])
    }
}
