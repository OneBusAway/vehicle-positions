import Foundation
import Testing
import VehiclePositionsKit
@testable import VehicleTracker

@Suite struct TripSessionTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let defaults = UserDefaults(suiteName: "TripSessionTests-\(UUID().uuidString)")!

    func session() -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store, locations: locations,
                    makeAPI: { _, _ in api }, now: { clock.now })
    }

    let bus = Vehicle(id: "bus-1", label: "Bus 1")
    let server = URL(string: "https://positions.example.org")!

    /// A trip with no stops: `AdherenceEvaluator(trip:)` refuses it, so
    /// nothing can be judged against it.
    var unusableTrip: TripGeometry {
        var trip = TripFixtures.t1
        trip.stops = []
        return trip
    }

    /// What a relaunch finds: a fresh token, a known server and this driver's
    /// own trip in the store, which the session takes up as `.paused`.
    func pausedSession(tripID id: Int64 = 7, trip: TripGeometry = TripFixtures.t1) throws -> (session: TripSession, active: ActiveTrip) {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        defaults.set("d@test.com", forKey: "email")
        let active = ActiveTrip(serverTripID: id, vehicle: bus, trip: trip,
                                startedAt: clock.now.addingTimeInterval(-600), driverEmail: "d@test.com")
        try store.save(active)
        return (session(), active)
    }

    @Test func startsSignedOutWithoutAToken() {
        #expect(session().phase == .signedOut)
    }

    @Test func staleTokenMeansSignedOut() throws {
        try tokens.save(StoredToken(token: "old", issuedAt: clock.now.addingTimeInterval(-25 * 3600)))
        #expect(session().phase == .signedOut)
    }

    @Test func freshTokenSkipsLogin() throws {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-3600)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        #expect(session().phase == .idle)
    }

    @Test func signInStoresTheTokenAndSettings() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(s.phase == .idle)
        #expect(api.logins.count == 1)
        #expect(try tokens.load()?.token == "jwt")
        #expect(s.settings.serverURLString == "https://positions.example.org")
        #expect(s.settings.email == "d@test.com")
    }

    @Test func signInFailureStaysSignedOut() async {
        api.loginError = APIError.status(401, message: "invalid credentials")
        let s = session()
        await #expect(throws: APIError.status(401, message: "invalid credentials")) {
            try await s.signIn(serverURL: server, email: "d@test.com", password: "bad")
        }
        #expect(s.phase == .signedOut)
    }

    @Test func startFetchesGeometryThenStartsTheServerTrip() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(api.tripRequests == ["T1"])
        #expect(api.starts.count == 1)
        #expect(api.starts[0] == ("bus-1", "R1", "T1"))
        guard case .active(let active) = s.phase else { Issue.record("expected active"); return }
        #expect(active.serverTripID == 42)
        #expect(active.vehicle == bus)
        #expect(active.trip == TripFixtures.t1)
        #expect(try store.load() == active)
        #expect(locations.handles.count == 1)
        #expect(locations.subscriptions == 1)
        #expect(s.settings.recentRouteIDs == ["R1"])
        #expect(s.settings.lastVehicleID == "bus-1")
    }

    @Test func startFailureReturnsToIdle() async throws {
        api.startError = APIError.status(409, message: "driver already has an active trip")
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        await #expect(throws: APIError.status(409, message: "driver already has an active trip")) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(s.phase == .idle)
        #expect(try store.load() == nil)
        #expect(locations.handles.isEmpty)
    }

    @Test func startWithAnUnusableTripThrowsBeforeStartingTheServerTrip() async throws {
        api.geometry = unusableTrip
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        await #expect(throws: TripSessionError.unusableTrip) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(api.starts.isEmpty, "the server trip must not have been started")
        #expect(try store.load() == nil)
        #expect(s.phase == .idle)
    }

    @Test func startWhileNotIdleThrowsBusy() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        await #expect(throws: TripSessionError.busy) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(api.starts.count == 1)
    }

    @Test func resumeWithAnUnusableStoredTripClearsIt() throws {
        let (s, stuck) = try pausedSession(trip: unusableTrip)
        #expect(s.phase == .paused(stuck))

        s.resume()
        #expect(s.phase == .idle)
        #expect(try store.load() == nil)
        #expect(locations.handles.isEmpty)
    }

    @Test func fixesDriveAdherenceAndThrottledReports() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.latest != nil })
        #expect(s.latest?.status == .onTime)
        #expect(await eventually { s.fixesSent == 1 })
        #expect(s.reporting == .connected)
        #expect(api.posted.count == 1)
        #expect(api.posted[0].tripID == "T1")
        #expect(api.posted[0].vehicleID == "bus-1")

        clock.advance(2)
        locations.emitFix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 2))
        // The second fix has been handled once the map shows it; one more
        // turn is all a send it had started would need to record itself.
        #expect(await eventually { s.latest?.fix.latitude == 47.6050 })
        await Task.yield()
        #expect(api.posted.count == 1, "a fix 2 s later updates the map but is not sent")

        clock.advance(4)
        locations.emitFix(lat: 47.6055, lon: -122.3300, at: TripFixtures.at(8, 5, 6))
        #expect(await eventually { api.posted.count == 2 })
        #expect(s.reporting == .connected)
        #expect(s.fixesSent == 2)
    }

    @Test func reportingStatusReflectsProblems() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        api.postError = APIError.transport("offline")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.reporting == .noNetwork })

        locations.emit(LocationSample(fix: nil, diagnostic: .locationUnavailable))
        #expect(await eventually { s.reporting == .noGPS })

        locations.emit(LocationSample(fix: nil, diagnostic: .insufficientlyInUse))
        #expect(await eventually { s.reporting == .needsForeground })

        clock.advance(5)
        api.postError = APIError.status(401, message: "invalid token")
        locations.emitFix(lat: 47.6046, lon: -122.3300, at: TripFixtures.at(8, 5, 5))
        #expect(await eventually { s.reporting == .authExpired })
        if case .active = s.phase {} else { Issue.record("a 401 keeps the trip running") }

        api.postError = nil
        try await s.reauthenticate(password: "pw")
        #expect(api.logins.count == 2)
        #expect(s.reporting == .connected, "reauthenticating clears the auth problem at once, before the next fix")
        clock.advance(5)
        locations.emitFix(lat: 47.6047, lon: -122.3300, at: TripFixtures.at(8, 5, 10))
        #expect(await eventually { s.fixesSent == 1 })
        #expect(s.reporting == .connected)
    }

    @Test func endStopsEverything() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.latest != nil })

        locations.emit(LocationSample(fix: nil, diagnostic: .locationUnavailable))
        #expect(await eventually { s.reporting == .noGPS })

        try await s.end()
        #expect(api.ends == [42])
        #expect(s.phase == .idle)
        #expect(s.latest == nil)
        #expect(try store.load() == nil)
        #expect(locations.handles[0].invalidated)
        #expect(s.reporting == .connected)
    }

    @Test func endFailureKeepsTheTripUntilEndedLocally() async throws {
        api.endError = APIError.transport("offline")
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        await #expect(throws: APIError.transport("offline")) { try await s.end() }
        if case .active = s.phase {} else { Issue.record("a failed end keeps the trip active") }
        #expect(!locations.handles[0].invalidated)

        s.endLocally()
        #expect(s.phase == .idle)
        #expect(try store.load() == nil)
        #expect(locations.handles[0].invalidated)
    }

    @Test func endIsNotReentrant() async throws {
        api.endDelay = .milliseconds(50)
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        let firstEnd = Task { try await s.end() }
        // Wait for the first call to move the phase to `.ending` before a
        // second one is attempted, so its early-return guard is exercised.
        #expect(await eventually { s.phase.isEnding },
                "expected .ending while the request is in flight")
        try await s.end()
        try await firstEnd.value

        #expect(api.ends.count == 1)
        #expect(s.phase == .idle)
    }

    @Test func endLocallyDuringAFailingEndWins() async throws {
        api.endError = APIError.transport("offline")
        api.endDelay = .milliseconds(50)
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        let ending = Task { try await s.end() }
        #expect(await eventually { s.phase.isEnding },
                "expected .ending while the request is in flight")
        s.endLocally()

        await #expect(throws: APIError.transport("offline")) { try await ending.value }
        #expect(s.phase == .idle, "endLocally wins: the failed end's restore is skipped because the phase already moved on")
        #expect(try store.load() == nil)
    }

    /// A successful `end()` must tear down the trip it was given, not
    /// whatever happens to be running by the time the server answers.
    @Test func aSucceedingEndDoesNotTearDownATripStartedSince() async throws {
        api.endDelay = .milliseconds(50)
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        let ending = Task { try await s.end() }
        #expect(await eventually { s.phase.isEnding },
                "expected .ending while the request is in flight")
        s.endLocally()
        api.startedID = 43
        try await s.start(vehicle: bus, tripID: "T1")

        try await ending.value
        if case .active(let current) = s.phase {
            #expect(current.serverTripID == 43, "the newer trip is left running")
        } else {
            Issue.record("the trip started while the end was in flight must still be active")
        }
        #expect(try store.load()?.serverTripID == 43, "the newer trip's record survives the older end")
        #expect(api.ends == [42], "only the trip the call was given was ended on the server")
    }

    /// A stream that dies while the trip is being ended, whose end then fails,
    /// must still read as lost once the phase is restored.
    @Test func aStreamLostDuringAFailingEndIsStillReported() async throws {
        api.endError = APIError.transport("offline")
        api.endDelay = .milliseconds(50)
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        let ending = Task { try await s.end() }
        #expect(await eventually { s.phase.isEnding },
                "expected .ending while the request is in flight")
        locations.finish(throwing: APIError.transport("dropped"))

        await #expect(throws: APIError.transport("offline")) { try await ending.value }
        if case .active = s.phase {} else { Issue.record("a failed end restores the active trip") }
        #expect(await eventually { s.reporting == .locationLost })
    }

    @Test func streamFailureIsReportedAndResumable() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.latest != nil })

        locations.finish(throwing: APIError.transport("dropped"))
        #expect(await eventually { s.reporting == .locationLost })
        if case .active = s.phase {} else { Issue.record("a lost stream keeps the trip active") }

        s.resume()
        #expect(locations.handles.count == 2, "resuming after a lost stream re-subscribes without ending the trip")
        #expect(locations.handles[0].invalidated, "the superseded background session is released, not leaked")
        #expect(!locations.handles[1].invalidated)
        #expect(api.starts.count == 1, "resuming after a lost stream makes no server call")
        #expect(await eventually { s.reporting == .connected })

        locations.emitFix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 5))
        #expect(await eventually { api.posted.count == 1 })
    }

    @Test func relaunchWithAStoredTripIsPausedAndResumes() async throws {
        let (s, active) = try pausedSession()
        #expect(s.phase == .paused(active))
        #expect(locations.handles.isEmpty)

        s.resume()
        #expect(s.phase == .active(active))
        #expect(api.starts.isEmpty, "resume does not start a second server trip")
        #expect(locations.handles.count == 1)

        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { api.posted.count == 1 })
        #expect(api.posted[0].tripID == "T1")

        try await s.end()
        #expect(api.ends == [7])
    }

    /// Spec §8: a fix the phone cannot trust is no position at all. It must
    /// not move the map and must not be reported, and the driver has to be
    /// told rather than left looking at a stale bus that reads as live.
    @Test func accuracyLimitedFreezesAdherenceAndShowsNoGPS() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { api.posted.count == 1 })
        let good = s.latest

        clock.advance(10)
        locations.emit(LocationSample(
            fix: TripFixtures.fix(lat: 47.6060, lon: -122.3300, at: TripFixtures.at(8, 5, 10), accuracy: 400),
            diagnostic: .accuracyLimited))
        #expect(await eventually { s.reporting == .noGPS })
        #expect(s.latest == good, "the map holds the last fix it could trust")
        await Task.yield()
        #expect(api.posted.count == 1, "an accuracy-limited fix is not reported")

        clock.advance(10)
        locations.emitFix(lat: 47.6065, lon: -122.3300, at: TripFixtures.at(8, 5, 20))
        #expect(await eventually { s.fixesSent == 2 })
        #expect(s.reporting == .connected)
        #expect(s.latest?.fix.latitude == 47.6065)
    }

    /// Phones are shared between shifts: a trip left in the store by the
    /// previous driver must never be taken up under the next one's account.
    @Test func aStoredTripForAnotherDriverIsDropped() async throws {
        let theirs = ActiveTrip(serverTripID: 7, vehicle: bus, trip: TripFixtures.t1,
                                startedAt: clock.now.addingTimeInterval(-600), driverEmail: "first@test.com")
        try store.save(theirs)

        let s = session()
        #expect(s.phase == .signedOut)
        try await s.signIn(serverURL: server, email: "second@test.com", password: "pw")
        #expect(s.phase == .idle, "signing in as someone else does not adopt their trip")
        #expect(try store.load() == nil, "and the record is dropped, not left to be adopted later")

        // A relaunch reads the same store and must make the same judgement.
        try store.save(theirs)
        let relaunched = session()
        #expect(relaunched.phase == .idle)
        #expect(try store.load() == nil)
    }

    /// `resume()` needs a server to report to. Signing out from `.paused`
    /// takes the API away while leaving the stored trip behind, so a resume
    /// afterwards has to say signed out rather than pretend to track.
    @Test func resumeWithoutAnAPIGoesToSignedOut() async throws {
        let (s, active) = try pausedSession()
        #expect(s.phase == .paused(active))
        s.signOut()
        #expect(s.phase == .signedOut)

        s.resume()
        #expect(s.phase == .signedOut)
        #expect(locations.handles.isEmpty, "nothing was subscribed to")
    }

    @Test func signOutClearsTheToken() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        s.signOut()
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
    }

    @Test func signOutFromPausedKeepsTheStoredTripForNextSignIn() async throws {
        let (s, active) = try pausedSession()
        #expect(s.phase == .paused(active))

        s.signOut()
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(try store.load() == active, "the stored trip survives a sign-out")

        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(s.phase == .paused(active))
    }
}
