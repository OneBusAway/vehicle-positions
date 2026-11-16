import Foundation
import Testing
import TipKit
@testable import VehicleTracker

/// TipKit set up once for the test process, with a store of its own: a
/// tip's parameters hold a value only once TipKit is configured, and the
/// app's own store is left alone.
enum TestTipKit {
    static let configured: Void = {
        let store = FileManager.default.temporaryDirectory.appending(path: "TipKit-\(UUID().uuidString)")
        try? FileManager.default.createDirectory(at: store, withIntermediateDirectories: true)
        try? Tips.configure([.datastoreLocation(.url(store))])
    }()
}

/// The two tips and First trip done (walkthrough spec §8): each once per
/// driver, one at a time. Real `UserDefaults` suites, so the flags are kept
/// the way the app keeps them.
@Suite struct DriverTipsTests {
    let defaults = UserDefaults(suiteName: "DriverTipsTests-\(UUID().uuidString)")!

    init() {
        _ = TestTipKit.configured
    }

    func settings(as email: String = "maria@metro.example") -> AppSettings {
        let settings = AppSettings(defaults: defaults)
        signIn(settings, as: email)
        return settings
    }

    func signIn(_ settings: AppSettings, to server: String = "https://tracker.example.org", as email: String) {
        settings.serverURLString = server
        settings.email = email
    }

    @Test(arguments: [DriverTips.Kind.runBoard, .sentCount])
    func eachTipShowsOncePerDriverAndAgainForTheNextDriver(kind: DriverTips.Kind) {
        let tips = DriverTips()
        let settings = settings()
        #expect(tips.mayShow(kind, settings: settings))
        tips.shown(kind)
        #expect(tips.showing == kind)
        #expect(tips.mayShow(kind, settings: settings), "it may stay up while it shows")
        tips.seen(kind, settings: settings)
        #expect(tips.showing == kind, "up until TipKit takes it down")
        tips.hidden(kind)
        #expect(tips.showing == nil)
        #expect(!tips.mayShow(kind, settings: settings))
        #expect(settings.hasSeen(kind.flag))

        signIn(settings, as: "omar@metro.example")
        #expect(tips.mayShow(kind, settings: settings), "the next driver on a shared phone")
        signIn(settings, as: "maria@metro.example")
        #expect(!tips.mayShow(kind, settings: settings))
        let relaunched = AppSettings(defaults: defaults)
        #expect(!DriverTips().mayShow(kind, settings: relaunched), "after a relaunch")
    }

    /// A driver who taps a run before the tip comes up has found what it
    /// says: it never comes.
    @Test func seenBeforeItShowsMeansItNeverShows() {
        let tips = DriverTips()
        let settings = settings()
        tips.seen(.runBoard, settings: settings)
        #expect(!tips.mayShow(.runBoard, settings: settings))
        #expect(tips.showing == nil)
        #expect(tips.mayShow(.sentCount, settings: settings), "the other tip is its own")
    }

    @Test func onlyOneTipIsUpAtATime() {
        let tips = DriverTips()
        let settings = settings()
        tips.shown(.runBoard)
        #expect(!tips.mayShow(.sentCount, settings: settings))
        #expect(!tips.mayShow(.firstTripDone, settings: settings))
        tips.seen(.runBoard, settings: settings)
        #expect(!tips.mayShow(.sentCount, settings: settings), "not while the last one is still going")
        tips.hidden(.runBoard)
        #expect(tips.mayShow(.sentCount, settings: settings))
        tips.shown(.sentCount)
        #expect(!tips.mayShow(.runBoard, settings: self.settings(as: "omar@metro.example")), "nor for another driver")
        tips.hidden(.runBoard)
        #expect(tips.showing == .sentCount, "only the tip that is up can be taken down")
        tips.hidden(.sentCount)
        #expect(tips.showing == nil)
    }

    /// Waiting to show, First trip done keeps the tips back too.
    @Test func firstTripDoneGoesBeforeAnyTip() {
        let tips = DriverTips()
        let settings = settings()
        tips.tripEnded(sent: 142, settings: settings)
        #expect(!tips.mayShow(.runBoard, settings: settings))
        #expect(tips.mayShow(.firstTripDone, settings: settings))
        tips.finishedTripShown(settings: settings)
        #expect(tips.showing == .firstTripDone)
        #expect(!tips.mayShow(.runBoard, settings: settings))
        tips.finishedTripClosed()
        #expect(tips.showing == nil)
        #expect(tips.mayShow(.runBoard, settings: settings))
    }

    @Test func firstTripDoneShowsOnceWithTheTripsCount() throws {
        let tips = DriverTips()
        let settings = settings()
        tips.tripEnded(sent: 142, settings: settings)
        let trip = try #require(tips.finishedTrip)
        #expect(trip.sent == 142)
        tips.finishedTripShown(settings: settings)
        #expect(settings.hasSeen(.firstTripDone), "shown is seen, however it closes")
        tips.finishedTripClosed()
        #expect(tips.finishedTrip == nil)

        tips.tripEnded(sent: 300, settings: settings)
        #expect(tips.finishedTrip == nil, "the second trip")
        signIn(settings, as: "omar@metro.example")
        tips.tripEnded(sent: 12, settings: settings)
        #expect(tips.finishedTrip?.sent == 12, "the next driver's first trip")
    }

    /// A trip that sent nothing gets no sheet, and leaves it for the first
    /// trip that does.
    @Test func firstTripDoneIsNotShownAtZero() {
        let tips = DriverTips()
        let settings = settings()
        tips.tripEnded(sent: 0, settings: settings)
        #expect(tips.finishedTrip == nil)
        #expect(!settings.hasSeen(.firstTripDone))
        tips.tripEnded(sent: 1, settings: settings)
        #expect(tips.finishedTrip?.sent == 1)
    }

    /// Tracking's tip is for the first trip only: once it ends, sent or
    /// not, the tip is done.
    @Test func theEndOfTheFirstTripIsTheEndOfTrackingsTip() {
        let tips = DriverTips()
        let settings = settings()
        tips.shown(.sentCount)
        tips.tripEnded(sent: 0, settings: settings)
        #expect(tips.showing == nil)
        #expect(!tips.mayShow(.sentCount, settings: settings))
        #expect(tips.mayShow(.runBoard, settings: settings), "the run board's tip is not about the trip")
    }

    /// The tip can still be up when the server confirms the end. The end
    /// takes it down for good: `wanted` is TipKit's, for the whole app, and
    /// left true it would bring the tip back on the next trip, on the road.
    @Test func theEndOfTheFirstTripTakesTrackingsTipDownForGood() throws {
        let tips = DriverTips()
        let settings = settings()
        SentCountTip.wanted = true
        try #require(SentCountTip.wanted, "TipKit holds the parameter")
        tips.shown(.sentCount)
        tips.tripEnded(sent: 12, settings: settings)
        #expect(!SentCountTip.wanted)
        #expect(settings.hasSeen(.sentCountTip))
        #expect(tips.showing == nil)
    }

    /// Closing a tip already marked seen still takes it down.
    @Test func aSeenTipStillComesDownWhenClosed() throws {
        let tips = DriverTips()
        let settings = settings()
        settings.markSeen(.sentCountTip)
        SentCountTip.wanted = true
        try #require(SentCountTip.wanted, "TipKit holds the parameter")
        SentCountTip.close(.actionPerformed, settings: settings, tips: tips)
        #expect(!SentCountTip.wanted)
    }

    /// Tracking's sheet is built again when the pickers go, so its tip
    /// waits for them to be gone.
    @Test func thePickersSayWhenTheyAreOnScreen() {
        let tips = DriverTips()
        #expect(!tips.pickersOnScreen, "a launch onto Tracking has none to wait for")
        tips.pickers(onScreen: true)
        #expect(tips.pickersOnScreen)
        tips.pickers(onScreen: false)
        #expect(!tips.pickersOnScreen)
    }

    /// TipKit keeps its record by tip id, per phone: an id with the
    /// driver's key in it is a record per driver. The key is the flags' own
    /// hash, so no email goes into TipKit's store.
    @Test func eachDriverHasTheirOwnTipsInTipKit() {
        let maria = DriverTips.key(of: settings(as: "maria@metro.example"))
        let omar = DriverTips.key(of: settings(as: "omar@metro.example"))
        #expect(maria == AppSettings.driverKey(host: "tracker.example.org", email: "maria@metro.example"))
        #expect(RunBoardTip(driver: maria).id == RunBoardTip(driver: maria).id)
        #expect(RunBoardTip(driver: maria).id != RunBoardTip(driver: omar).id)
        #expect(SentCountTip(driver: maria).id != SentCountTip(driver: omar).id)
        #expect(RunBoardTip(driver: maria).id != SentCountTip(driver: maria).id)
        #expect(!RunBoardTip(driver: maria).id.contains("maria"))
        #expect(!SentCountTip(driver: maria).id.contains("maria"))
    }

    // MARK: Where the run board's tip points

    func run(_ id: String, _ hour: Int, _ minute: Int) -> TripSummary {
        let start = TripFixtures.at(hour, minute)
        return TripSummary(id: id, headsign: "Downtown", directionID: nil, startsAt: start,
                           endsAt: start.addingTimeInterval(10 * 60), firstStop: "A", lastStop: "B")
    }

    func tipRun(at hour: Int, _ minute: Int, _ runs: [TripSummary]) -> String? {
        let page = RouteTripsPage(routeID: "R7", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: runs)
        return RunBoards.of(page, now: TripFixtures.at(hour, minute)).boards[0].tipRun
    }

    var day: [TripSummary] { [run("a", 6, 28), run("b", 6, 43), run("c", 6, 58), run("d", 7, 13)] }

    @Test func theTipPointsAtTheRunUnderWay() {
        #expect(tipRun(at: 7, 0, day) == "c")
    }

    @Test func withNoRunUnderWayItPointsAtTheNextOne() {
        #expect(tipRun(at: 6, 0, day) == "a", "before the first run")
        #expect(tipRun(at: 6, 52, day) == "c", "between two runs, the one starting nearest")
    }

    /// Once every run is over the board opens at the last one, at the top of
    /// the screen: the first run of the day is scrolled out of sight.
    @Test func afterTheDayItPointsAtTheFirstRunOnScreen() {
        #expect(tipRun(at: 9, 0, day) == "d")
    }

    @Test func anEmptyBoardHasNoRunToPointAt() {
        #expect(tipRun(at: 7, 0, []) == nil)
    }
}

/// Tracking, Resume and the car end a trip through `DriverTips.end`: the
/// count First trip done shows is the session's, read once the server has
/// confirmed the end, and the session keeps it until the next trip starts.
@Suite struct TripEndTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let defaults = UserDefaults(suiteName: "TripEndTests-\(UUID().uuidString)")!

    init() {
        _ = TestTipKit.configured
    }

    /// A trip under way that has sent two reports.
    func tripWithTwoReports() async throws -> TripSession {
        let session = TripSession(settings: AppSettings(defaults: defaults), tokens: InMemoryTokenStore(), store: InMemoryActiveTripStore(),
                                  locations: locations, access: FakeLocationAccess(), makeAPI: { _, _ in api }, now: { clock.now })
        try await session.signIn(serverURL: URL(string: "https://tracker.example.org")!, email: "maria@metro.example", password: "pw")
        try await session.start(vehicle: Vehicle(id: "4317", label: "4317"), tripID: "T1")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { session.fixesSent == 1 })
        clock.advance(6)
        locations.emitFix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 6))
        #expect(await eventually { session.fixesSent == 2 })
        return session
    }

    @Test func theEndedTripsCountReachesTheSheet() async throws {
        let session = try await tripWithTwoReports()
        let tips = DriverTips()
        try await tips.end(session)
        #expect(session.phase == .idle)
        #expect(tips.finishedTrip?.sent == 2)
        #expect(session.settings.hasSeen(.sentCountTip), "the first trip is over")
    }

    /// The trip is still the driver's: nothing changes for the tips.
    @Test func anEndTheServerDidNotConfirmLeavesTheTips() async throws {
        let session = try await tripWithTwoReports()
        api.endError = APIError.transport("offline")
        let tips = DriverTips()
        await #expect(throws: APIError.self) { try await tips.end(session) }
        #expect(tips.finishedTrip == nil)
        #expect(!session.settings.hasSeen(.sentCountTip))
    }
}
