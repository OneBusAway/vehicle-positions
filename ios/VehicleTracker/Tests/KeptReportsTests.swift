import Foundation
import Observation
import SwiftUI
import Testing
import UIKit
import VehiclePositionsKit
@testable import VehicleTracker

/// Kept reports sent with no trip running, through a real `TripSession`: what
/// starts a drain, and whose reports a sign-in leaves.
@Suite struct KeptReportsTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let background = FakeBackgroundTime()
    /// The wait between two batches of a drain, fired by hand.
    let interval = ManualSleep()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "KeptReportsTests-\(UUID().uuidString)"
    let server = URL(string: "https://positions.example.org")!
    let vehicle = Vehicle(id: "bus-1", label: "Vehicle 1")

    var driver: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: server.absoluteString, email: "d@test.com")! }
    var other: OfflineFixQueue.Owner { OfflineFixQueue.Owner(server: server.absoluteString, email: "e@test.com")! }

    /// The app as a launch builds it over this test's folder and defaults.
    func launch() throws -> TripSession {
        let settings = AppSettings(defaults: try #require(UserDefaults(suiteName: defaultsName)))
        let history = HistoryUploader(queue: OfflineFixQueue(directory: directory), settings: settings, now: { clock.now },
                                      backgroundTime: background, sleep: interval.sleep(for:))
        return TripSession(settings: settings, tokens: tokens, store: store, locations: locations,
                           access: FakeLocationAccess(), history: history, makeAPI: { _, _ in api }, now: { clock.now })
    }

    /// A launch that finds d@test.com still signed in, with no trip.
    func launchSignedIn() throws -> TripSession {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        let defaults = try #require(UserDefaults(suiteName: defaultsName))
        defaults.set(server.absoluteString, forKey: "serverURL")
        defaults.set("d@test.com", forKey: "email")
        let session = try launch()
        try #require(session.phase == .idle)
        return session
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    func report(_ ago: Int64) -> LocationReport {
        LocationReport(vehicleID: "bus-1", tripID: "T1", latitude: 47.6, longitude: -122.33,
                       bearing: 90, speed: 8, accuracy: 5, timestamp: Int64(clock.now.timeIntervalSince1970) - ago)
    }

    /// Reports kept by an earlier launch, as the files hold them.
    func kept(_ count: Int, for owner: OfflineFixQueue.Owner) async throws {
        let queue = OfflineFixQueue(directory: directory)
        for ago in (1...count).reversed() {
            try await queue.add(report(Int64(ago) * 5), for: owner)
        }
    }

    /// What is left on disk, as the next launch would find it.
    func left(for owner: OfflineFixQueue.Owner) async throws -> Int {
        try await OfflineFixQueue(directory: directory).oldest(.max, for: owner).count
    }

    /// Lets a drain that has nothing more to send finish, and waits for it.
    func drainIsOver() async -> Bool {
        interval.fire()
        return await eventually { background.running == 0 }
    }

    /// A trip that is not reporting for want of a connection, with one
    /// report kept.
    func tripWithAReportKept(_ session: TripSession) async throws {
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try #require(await eventually { background.running == 0 })
        try await session.start(vehicle: vehicle, tripID: "T1")
        api.postError = APIError.transport("offline")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        try #require(await eventually { session.reporting == .noNetwork })
        try #require(try await left(for: driver) == 1)
    }

    // MARK: When a drain starts

    /// The trip's last stretch without a connection goes as it ends, though
    /// no report got through to take it, rather than with this driver's next
    /// trip, which may be over a day away.
    @Test func endingATripSendsWhatItKeptWithNoTripOpen() async throws {
        defer { cleanUp() }
        let session = try launch()
        try await tripWithAReportKept(session)
        api.postError = nil

        try await session.end()
        #expect(await eventually { api.historyUploads.count == 1 })
        #expect(api.historyUploads.first?.points.map(\.timestamp) == [Int64(TripFixtures.at(8, 5).timeIntervalSince1970)])
        #expect(api.ends == [42])
        #expect(session.phase == .idle)
        #expect(await drainIsOver())
        #expect(try await left(for: driver) == 0)
    }

    /// The server could not be told of the end, but the reports need no open
    /// trip, so they go as soon as they can.
    @Test func endingATripOnThisPhoneSendsWhatItKeptToo() async throws {
        defer { cleanUp() }
        let session = try launch()
        try await tripWithAReportKept(session)
        api.postError = nil

        session.endLocally()
        #expect(await eventually { api.historyUploads.count == 1 })
        #expect(api.ends.isEmpty)
        #expect(await drainIsOver())
        #expect(try await left(for: driver) == 0)
    }

    /// Kept before a relaunch, by a trip that ended in a dead zone: the sign-in
    /// sends them, with no trip.
    @Test func aSignInSendsWhatWasKeptForThisDriver() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()
        #expect(api.historyUploads.isEmpty, "a launch signed out sends nothing")

        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        #expect(session.phase == .idle)
        #expect(await drainIsOver())
        #expect(try await left(for: driver) == 0)
    }

    /// What `RootView` calls each time the app comes to the front.
    @Test func sendingKeptReportsWithNoTripSendsThem() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launchSignedIn()
        #expect(api.historyUploads.isEmpty, "a launch alone sends nothing")

        session.sendKeptReports()
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        #expect(await drainIsOver())
    }

    /// During a trip each report that gets through takes a batch with it;
    /// the app coming to the front adds none.
    @Test func noDrainDuringATrip() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launchSignedIn()
        try await session.start(vehicle: vehicle, tripID: "T1")

        session.sendKeptReports()
        #expect(await eventually { background.begun.count == 1 && background.running == 0 })
        #expect(api.historyUploads.isEmpty)
        #expect(interval.asked.isEmpty)

        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] }, "the reporter's batch")
    }

    /// A drain that is still going when a trip starts leaves the rest to it.
    @Test func aDrainStopsOnceATripStarts() async throws {
        defer { cleanUp() }
        try await kept(600, for: driver)
        let session = try launchSignedIn()

        session.sendKeptReports()
        #expect(await eventually { interval.asked.count == 1 })
        #expect(api.historyUploads.map(\.points.count) == [500])
        try await session.start(vehicle: vehicle, tripID: "T1")
        #expect(await drainIsOver())
        #expect(api.historyUploads.count == 1)
        #expect(try await left(for: driver) == 100)
    }

    // MARK: Whose reports a sign-in leaves

    /// A phone handed on: the next driver's sign-in drops what was kept for
    /// the driver before, who can't send it from here again, even by signing
    /// back in later.
    @Test func anotherDriverSigningInDropsTheReportsKeptForTheLast() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()
        api.historyError = APIError.transport("offline")
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try #require(await eventually { background.running == 0 })
        try #require(api.historyUploads.count == 1, "kept, as there was no connection")
        await session.signOut()
        api.historyError = nil

        try await session.signIn(serverURL: server, email: "e@test.com", password: "pw")
        #expect(try await left(for: driver) == 0)
        await session.signOut()
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.count == 1, "nothing left to send as d@test.com")
    }

    /// One driver and server, however the driver types them.
    @Test func theSameDriverRetypedKeepsAndSendsTheirReports() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()

        try await session.signIn(serverURL: URL(string: "https://Positions.example.org/")!, email: " D@Test.com ",
                                 password: "pw")
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        #expect(await drainIsOver())
    }

    /// Signing out hands the phone to nobody yet, so the reports stay for
    /// the same driver signing back in.
    @Test(arguments: [true, false])
    func signingOutKeepsThemForTheSameDriver(tellingTheServer: Bool) async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()
        api.historyError = APIError.transport("offline")
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try #require(await eventually { background.running == 0 })

        if tellingTheServer {
            await session.signOut()
        } else {
            session.endSessionLocally()
        }
        try #require(session.phase == .signedOut)
        #expect(try await left(for: driver) == 2)

        api.historyError = nil
        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(await eventually { api.historyUploads.last?.points.count == 2 && api.historyUploads.count == 2 })
        #expect(await drainIsOver())
        #expect(try await left(for: driver) == 0)
    }

    /// A trip id on one server means nothing on another, so the same driver
    /// signing in elsewhere is another owner.
    @Test func signingInToAnotherServerDropsTheOldServersReports() async throws {
        defer { cleanUp() }
        try await kept(2, for: driver)
        let session = try launch()

        try await session.signIn(serverURL: URL(string: "https://other.example.org")!, email: "d@test.com", password: "pw")
        #expect(try await left(for: driver) == 0)
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.isEmpty)
    }

    /// Files that can't be removed cost nothing worse than the space: each is
    /// kept under its own driver, so it can never go as this one's.
    @Test func aSignInGoesOnWhenTheReportsOfOthersCannotBeDropped() async throws {
        defer { cleanUp() }
        try await kept(2, for: other)
        let folder = directory.appending(path: "offline-fixes").path(percentEncoded: false)
        try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: folder)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: folder) }
        let session = try launch()

        try await session.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(session.phase == .idle)
        #expect(try await left(for: other) == 2)
        #expect(await eventually { background.running == 0 })
        #expect(api.historyUploads.isEmpty)
    }
}

/// `RootView` starts a drain each time the phone's window comes to the
/// front.
@Suite struct KeptReportsInFrontTests {
    @Observable final class Window {
        var phase: ScenePhase
        /// The phase the view was last drawn with; nil until it appears.
        var drawn: ScenePhase?

        init(_ phase: ScenePhase) {
            self.phase = phase
        }
    }

    struct Content: View {
        let window: Window
        let session: TripSession
        let setup: ServerSetup

        var body: some View {
            RootView()
                .environment(session)
                .environment(setup)
                .environment(\.scenePhase, window.phase)
                .onChange(of: window.phase, initial: true) { _, phase in window.drawn = phase }
        }
    }

    let api = FakeTrackerAPI()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let background = FakeBackgroundTime()
    let interval = ManualSleep()
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let defaultsName = "KeptReportsInFrontTests-\(UUID().uuidString)"

    /// A relaunch onto Resume your trip?, with two reports kept for its driver.
    func pausedSession() async throws -> TripSession {
        let now = Date()
        try tokens.save(StoredToken(token: "jwt", issuedAt: now.addingTimeInterval(-60)))
        let defaults = try #require(UserDefaults(suiteName: defaultsName))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        defaults.set("d@test.com", forKey: "email")
        try store.save(ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "bus-1", label: "Vehicle 1"), trip: TripFixtures.t1,
                                  startedAt: now.addingTimeInterval(-600), driverEmail: "d@test.com"))
        let queue = OfflineFixQueue(directory: directory)
        let owner = try #require(OfflineFixQueue.Owner(server: "https://positions.example.org", email: "d@test.com"))
        for ago: Int64 in [20, 10] {
            try await queue.add(LocationReport(vehicleID: "bus-1", tripID: "T1", latitude: 47.6, longitude: -122.33,
                                               bearing: 90, speed: 8, accuracy: 5,
                                               timestamp: Int64(now.timeIntervalSince1970) - ago), for: owner)
        }
        let settings = AppSettings(defaults: defaults)
        let session = TripSession(settings: settings, tokens: tokens, store: store, locations: FakeLocationSource(),
                                  access: FakeLocationAccess(),
                                  history: HistoryUploader(queue: queue, settings: settings, backgroundTime: background,
                                                           sleep: interval.sleep(for:)),
                                  makeAPI: { _, _ in api })
        try #require(session.phase != .signedOut)
        return session
    }

    func cleanUp() {
        try? FileManager.default.removeItem(at: directory)
        UserDefaults(suiteName: defaultsName)?.removePersistentDomain(forName: defaultsName)
    }

    /// Puts `RootView` on screen under `window.phase`, as the app's window
    /// would have it, and waits until it has appeared.
    func show(_ window: Window, session: TripSession) async throws -> UIWindow {
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let shown = UIWindow(windowScene: scene)
        shown.rootViewController = UIHostingController(
            rootView: Content(window: window, session: session, setup: ServerSetup(session: session)))
        shown.isHidden = false
        try #require(await eventually { window.drawn != nil })
        return shown
    }

    @Test func comingToTheFrontSendsWhatWasKept() async throws {
        defer { cleanUp() }
        let session = try await pausedSession()
        let window = Window(.background)
        let shown = try await show(window, session: session)
        #expect(background.begun.isEmpty, "a window that appears behind hasn't come to the front")

        window.phase = .active
        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        interval.fire()
        #expect(await eventually { background.running == 0 })
        withExtendedLifetime(shown) {}
    }

    /// A launch the driver makes can come up already in front, with no
    /// change to see.
    @Test func aWindowFirstShownInFrontSendsThemToo() async throws {
        defer { cleanUp() }
        let session = try await pausedSession()
        let window = Window(.active)
        let shown = try await show(window, session: session)

        #expect(await eventually { api.historyUploads.map(\.points.count) == [2] })
        interval.fire()
        #expect(await eventually { background.running == 0 })
        withExtendedLifetime(shown) {}
    }
}
