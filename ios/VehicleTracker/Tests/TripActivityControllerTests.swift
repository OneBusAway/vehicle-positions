import ActivityKit
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
    /// The heartbeat's wait, which the test ends by hand.
    let sleeper = ManualSleep()
    let bus = Vehicle(id: "bus-1", label: "Bus 1")

    func session() -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store, locations: locations,
                    access: access, makeAPI: { _, _ in api }, now: { clock.now })
    }

    func controller(_ session: TripSession) -> TripActivityController {
        TripActivityController(session: session, host: host, now: { clock.now }, sleep: sleeper.sleep(for:))
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

    /// What a relaunch finds: a fresh token, a known server and trip 7 in
    /// the store, which the session takes up as `.paused`.
    func relaunched() throws -> TripSession {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        defaults.set("d@test.com", forKey: "email")
        try store.save(ActiveTrip(serverTripID: 7, vehicle: bus, trip: TripFixtures.t1,
                                  startedAt: clock.now.addingTimeInterval(-600), driverEmail: "d@test.com"))
        return session()
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

    @Test func startsAtTheNextForegroundOnceTheyAreOn() async throws {
        host.areActivitiesEnabled = false
        let s = session()
        let c = controller(s)
        try await s.signIn(serverURL: URL(string: "https://positions.example.org")!, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        await emitAndSettle(s, secondsLate: 0)

        // Turned on in Settings: the driver comes back to the app.
        host.areActivitiesEnabled = true
        await emitAndSettle(s, secondsLate: 360)
        #expect(host.requests.isEmpty, "not asked again on every fix")
        c.sceneBecameActive()
        #expect(host.requests.count == 1)
        #expect(host.requests.first?.state.lead == "6 min late")
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

    // MARK: Relaunch, the car and the driver's choice

    /// iOS closed the app during trip 7 and left its activity up: the
    /// relaunch takes it over, with no second card, and says the trip is
    /// paused.
    @Test func adoptsTheActivityForTheStoredTripOnRelaunch() async throws {
        host.listed = [.init(id: "before", serverTripID: 7, state: .active)]
        let s = try relaunched()
        let c = controller(s)
        #expect(await eventually { host.updates.count == 1 })
        #expect(host.updates.first?.id == "before")
        #expect(host.updates.first?.state.lead == "Location stopped")

        s.resume()
        #expect(await eventually { host.updates.last?.state.lead == "Waiting for GPS" })
        #expect(host.requests.isEmpty)
        #expect(host.ends.isEmpty)
        withExtendedLifetime(c) {}
    }

    @Test func endsActivitiesForOtherTripsOnRelaunch() async throws {
        host.listed = [.init(id: "before", serverTripID: 7, state: .active),
                       .init(id: "stray", serverTripID: 3, state: .active),
                       .init(id: "swiped", serverTripID: 4, state: .dismissed)]
        let s = try relaunched()
        let c = controller(s)
        #expect(await eventually { host.ends == ["stray"] })
        #expect(await eventually { host.updates.first?.id == "before" })
        withExtendedLifetime(c) {}
    }

    @Test func doesNotRestartATripTheDriverSwipedAway() async throws {
        let (s, c) = try await started()
        host.set(.dismissed, of: "activity-1")
        await emitAndSettle(s, secondsLate: 0)
        await emitAndSettle(s, secondsLate: 360)
        #expect(host.requests.count == 1)
        #expect(host.updates.isEmpty)
        #expect(host.ends.isEmpty, "it is already gone")
        withExtendedLifetime(c) {}
    }

    /// The system ended trip 7's activity at its eight hours while the app
    /// was closed: resuming the trip doesn't bring it back.
    @Test func doesNotRestartAfterTheSystemEndedIt() async throws {
        host.listed = [.init(id: "before", serverTripID: 7, state: .ended)]
        let s = try relaunched()
        let c = controller(s)
        s.resume()
        await emitAndSettle(s, secondsLate: 0)
        #expect(host.requests.isEmpty)
        #expect(host.updates.isEmpty)
        #expect(host.ends.isEmpty)
        withExtendedLifetime(c) {}
    }

    /// Started from the car with the phone app in the background, where
    /// ActivityKit refuses: tried once more when the phone app comes to the
    /// front, not on every fix.
    @Test func retriesAStartTheBackgroundRefusedAtTheNextForeground() async throws {
        host.inForeground = false
        let (s, c) = try await started()
        await emitAndSettle(s, secondsLate: 0)
        await emitAndSettle(s, secondsLate: 360)
        #expect(host.requests.count == 1)

        host.inForeground = true
        c.sceneBecameActive()
        #expect(host.requests.count == 2)
        #expect(host.requests.last?.state.lead == "6 min late")
        c.sceneBecameActive()
        #expect(host.requests.count == 2, "one activity is enough")
    }

    @Test func aNewTripAfterADeclinedOneStartsAgain() async throws {
        let (s, c) = try await started()
        host.set(.dismissed, of: "activity-1")
        await emitAndSettle(s, secondsLate: 0)
        try await s.end()
        api.startedID = 43
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(await eventually { host.requests.count == 2 })
        #expect(host.requests.last?.attributes.serverTripID == 43)
        withExtendedLifetime(c) {}
    }

    // MARK: Stale

    /// The app is the only updater: if it stops, the activity shows it two
    /// minutes after the last thing it was sent.
    @Test func everyRequestAndUpdateIsStaleTwoMinutesLater() async throws {
        let (s, c) = try await started()
        #expect(host.requests.first?.staleDate == TripFixtures.at(8, 2))
        clock.advance(10)
        await emitAndSettle(s, secondsLate: 360)
        #expect(await eventually { host.updates.count == 1 })
        #expect(host.updates.first?.staleDate == TripFixtures.at(8, 2, 10))
        withExtendedLifetime(c) {}
    }

    @Test func theHeartbeatResendsTheSameStateAfterAMinute() async throws {
        let (_, c) = try await started()
        #expect(await eventually { sleeper.asked == [.seconds(60)] })
        clock.advance(60)
        sleeper.fire()
        #expect(await eventually { host.updates.count == 1 })
        let resent = try #require(host.updates.first)
        #expect(resent.id == "activity-1")
        #expect(resent.state == host.requests.first?.state)
        #expect(resent.staleDate == TripFixtures.at(8, 3))
        #expect(await eventually { sleeper.asked.count == 2 })
        #expect(sleeper.asked.last == .seconds(60))
        withExtendedLifetime(c) {}
    }

    /// A change half a minute in moves the next heartbeat to a minute after
    /// it, not after the start.
    @Test func noHeartbeatWithinAMinuteOfAChange() async throws {
        let (s, c) = try await started()
        #expect(await eventually { sleeper.asked.count == 1 })
        clock.advance(30)
        await emitAndSettle(s, secondsLate: 360)
        #expect(await eventually { host.updates.count == 1 })

        clock.advance(30)
        sleeper.fire()
        #expect(await eventually { sleeper.asked.count == 2 })
        #expect(sleeper.asked.last == .seconds(30))
        #expect(host.updates.count == 1)

        clock.advance(30)
        sleeper.fire()
        #expect(await eventually { host.updates.count == 2 })
        #expect(host.updates.last?.state.lead == "6 min late")
        withExtendedLifetime(c) {}
    }

    @Test func theHeartbeatStopsWhenTheTripEnds() async throws {
        let (s, c) = try await started()
        #expect(await eventually { sleeper.asked.count == 1 })
        try await s.end()
        #expect(await eventually { host.ends == ["activity-1"] })
        clock.advance(60)
        sleeper.fire()
        for _ in 0..<20 { await Task.yield() }
        #expect(sleeper.asked.count == 1, "no heartbeat waits once the activity is gone")
        #expect(host.updates.isEmpty)
        withExtendedLifetime(c) {}
    }
}
