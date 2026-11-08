import Foundation
import Testing
import VehiclePositionsKit
@testable import VehicleTracker

@Suite struct TripActivityControllerTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let access = FakeLocationAccess()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let defaults = UserDefaults(suiteName: "TripActivityControllerTests-\(UUID().uuidString)")!
    let host = FakeActivityHost()
    let bus = Vehicle(id: "bus-1", label: "Bus 1")

    func session() -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store, locations: locations,
                    access: access, makeAPI: { _, _ in api }, now: { clock.now })
    }

    func controller(_ session: TripSession) -> TripActivityController {
        TripActivityController(session: session, host: host)
    }

    /// The controller is there from launch, as in `AppContainer`; then the
    /// driver signs in and starts trip 42.
    func started() async throws -> (TripSession, TripActivityController) {
        let s = session()
        let c = controller(s)
        try await s.signIn(serverURL: URL(string: "https://positions.example.org")!, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(await eventually { host.requests.count == 1 })
        return (s, c)
    }

    /// At ST1, `secondsLate` behind its 08:00: on time up to five minutes,
    /// late beyond.
    func emit(secondsLate: TimeInterval, north metres: Double = 0) {
        locations.emitFix(lat: 47.6000 + metres / 111_000, lon: -122.3300,
                          at: TripFixtures.at(8, 0).addingTimeInterval(secondsLate))
    }

    /// Emits a fix and waits until the controller has seen it: the session
    /// takes it, then the controller's observation runs on the main actor.
    func emitAndSettle(_ s: TripSession, secondsLate: TimeInterval, north metres: Double = 0) async {
        let target = TripFixtures.at(8, 0).addingTimeInterval(secondsLate)
        emit(secondsLate: secondsLate, north: metres)
        _ = await eventually { s.latest?.fix.timestamp == target }
        for _ in 0..<20 { await Task.yield() }
    }

    // MARK: Starting

    @Test func startsOneActivityWhenTheTripStarts() async throws {
        let (s, c) = try await started()
        let request = try #require(host.requests.first)
        #expect(request.attributes.serverTripID == 42)
        #expect(request.attributes.routeName == "1")
        #expect(request.attributes.routeColor == "0077C0")
        #expect(request.attributes.routeTextColor == "FFFFFF")
        #expect(request.attributes.headsign == "North")
        #expect(request.state.lead == "Waiting for GPS")

        await emitAndSettle(s, secondsLate: 0)
        await emitAndSettle(s, secondsLate: 360)
        #expect(await eventually { host.updates.count == 2 })
        #expect(host.requests.count == 1, "one activity for the trip, however many fixes")
        withExtendedLifetime(c) {}
    }

    /// Off in Settings: nothing is asked of ActivityKit, so nothing shows.
    @Test func doesNothingWhileLiveActivitiesAreOff() async throws {
        host.areActivitiesEnabled = false
        let s = session()
        let c = controller(s)
        try await s.signIn(serverURL: URL(string: "https://positions.example.org")!, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        await emitAndSettle(s, secondsLate: 0)
        await emitAndSettle(s, secondsLate: 360)
        #expect(host.requests.isEmpty)
        #expect(host.updates.isEmpty)
        withExtendedLifetime(c) {}
    }

    // MARK: Updating

    /// Ten metres on, in the same minute: the words are the same, so nothing
    /// is sent. The late fix after it shows the controller had seen it.
    @Test func fixesWithTheSameWordsSendNothing() async throws {
        let (s, c) = try await started()
        await emitAndSettle(s, secondsLate: 0)
        await emitAndSettle(s, secondsLate: 10, north: 10)
        await emitAndSettle(s, secondsLate: 360)
        #expect(await eventually { host.updates.count == 2 })
        #expect(host.updates.map(\.state.lead) == ["On time", "6 min late"])
        withExtendedLifetime(c) {}
    }

    @Test func aChangedAnswerSendsOneUpdate() async throws {
        let (s, c) = try await started()
        await emitAndSettle(s, secondsLate: 360)
        #expect(await eventually { host.updates.count == 1 })
        let update = try #require(host.updates.first)
        #expect(update.id == "activity-1")
        #expect(update.state.lead == "6 min late")
        #expect(update.state.minutes == "6 min")
        withExtendedLifetime(c) {}
    }

    /// OneBusAway's `seriallyAppliesFirstAndLatestStateWhileApplyIsInFlight`:
    /// while the first update is held, two more states arrive; when it lands
    /// only the newest goes, and never alongside another.
    @Test func appliesTheFirstAndThenOnlyTheNewestWhileAnUpdateIsInFlight() async throws {
        let (s, c) = try await started()
        let gate = Gate()
        host.whileUpdating = { await gate.wait() }

        await emitAndSettle(s, secondsLate: 360)
        #expect(await eventually { host.updates.count == 1 })
        await emitAndSettle(s, secondsLate: 420)
        await emitAndSettle(s, secondsLate: 480)
        #expect(host.updates.count == 1, "nothing more goes while one is in flight")

        gate.open()
        #expect(await eventually { host.updates.count == 2 })
        // The same words again, to give a stray third update time to show.
        await emitAndSettle(s, secondsLate: 481, north: 1)
        #expect(host.updates.map(\.state.lead) == ["6 min late", "8 min late"])
        #expect(!host.overlapped)
        withExtendedLifetime(c) {}
    }

    // MARK: Ending

    @Test func endsImmediatelyWhenTheTripEnds() async throws {
        let (s, c) = try await started()
        try await s.end()
        #expect(s.phase == .idle)
        #expect(await eventually { host.ends == ["activity-1"] })
        withExtendedLifetime(c) {}
    }

    @Test func endsWhenEndLocally() async throws {
        let (s, c) = try await started()
        s.endLocally()
        #expect(await eventually { host.ends == ["activity-1"] })
        withExtendedLifetime(c) {}
    }

    @Test func keepsItWhileSignedOutAndShowsSignedOut() async throws {
        let (s, c) = try await started()
        api.postError = APIError.status(401, message: "invalid token")
        emit(secondsLate: 0)
        #expect(await eventually { s.reporting == .authExpired })
        #expect(await eventually { host.updates.last?.state.lead == "Signed out" })
        #expect(host.ends.isEmpty)
        withExtendedLifetime(c) {}
    }

    /// The end is out but not confirmed: the activity stays, saying so, and
    /// goes once the server answers.
    @Test func keepsItWhileEnding() async throws {
        let (s, c) = try await started()
        let gate = Gate()
        api.whileEnding = { await gate.wait() }
        let ending = Task { try await s.end() }
        #expect(await eventually { s.phase.isEnding })
        #expect(await eventually { host.updates.last?.state.reports == "Ending…" })
        #expect(host.ends.isEmpty)

        gate.open()
        try await ending.value
        #expect(await eventually { host.ends == ["activity-1"] })
        withExtendedLifetime(c) {}
    }
}
