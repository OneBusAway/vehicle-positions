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

    @Test func resumeWithAnUnusableStoredTripClearsIt() throws {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        let stuck = ActiveTrip(serverTripID: 7, vehicle: bus, trip: unusableTrip, startedAt: clock.now.addingTimeInterval(-600))
        try store.save(stuck)

        let s = session()
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
        #expect(await eventually { s.reporting == .connected(fixesSent: 1) })
        #expect(api.posted.count == 1)
        #expect(api.posted[0].tripID == "T1")
        #expect(api.posted[0].vehicleID == "bus-1")

        clock.advance(2)
        locations.emitFix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 2))
        #expect(await eventually { s.latest?.fix.latitude == 47.6050 })
        try? await Task.sleep(for: .milliseconds(100))
        #expect(api.posted.count == 1, "a fix 2 s later updates the map but is not sent")

        clock.advance(4)
        locations.emitFix(lat: 47.6055, lon: -122.3300, at: TripFixtures.at(8, 5, 6))
        #expect(await eventually { api.posted.count == 2 })
        #expect(s.reporting == .connected(fixesSent: 2))
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
        #expect(s.reporting == .connected(fixesSent: 0), "reauthenticating clears the auth problem at once, before the next fix")
        clock.advance(5)
        locations.emitFix(lat: 47.6047, lon: -122.3300, at: TripFixtures.at(8, 5, 10))
        #expect(await eventually { s.reporting == .connected(fixesSent: 1) })
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
        #expect(s.reporting == .connected(fixesSent: 0))
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
        // Give the first call time to move the phase to `.ending` before a
        // second one is attempted, so its early-return guard is exercised.
        try? await Task.sleep(for: .milliseconds(10))
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
        try? await Task.sleep(for: .milliseconds(10))
        if case .ending = s.phase {} else { Issue.record("expected .ending while the request is in flight") }
        s.endLocally()

        await #expect(throws: APIError.transport("offline")) { try await ending.value }
        #expect(s.phase == .idle, "endLocally wins: the failed end's restore is skipped because the phase already moved on")
        #expect(try store.load() == nil)
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
        #expect(api.starts.count == 1, "resuming after a lost stream makes no server call")
        #expect(await eventually { s.reporting == .connected(fixesSent: 0) })

        locations.emitFix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 5))
        #expect(await eventually { api.posted.count == 1 })
    }

    @Test func relaunchWithAStoredTripIsPausedAndResumes() async throws {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        let active = ActiveTrip(serverTripID: 7, vehicle: bus, trip: TripFixtures.t1, startedAt: clock.now.addingTimeInterval(-600))
        try store.save(active)

        let s = session()
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

    @Test func signOutClearsTheToken() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        s.signOut()
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
    }

    @Test func signOutFromPausedKeepsTheStoredTripForNextSignIn() async throws {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        let active = ActiveTrip(serverTripID: 7, vehicle: bus, trip: TripFixtures.t1, startedAt: clock.now.addingTimeInterval(-600))
        try store.save(active)

        let s = session()
        #expect(s.phase == .paused(active))

        s.signOut()
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(try store.load() == active, "the stored trip survives a sign-out")

        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(s.phase == .paused(active))
    }
}
