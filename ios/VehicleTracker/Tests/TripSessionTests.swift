import Foundation
import Testing
import VehiclePositionsKit
@testable import VehicleTracker

@Suite struct TripSessionTests {
    let api = FakeTrackerAPI()
    let locations = FakeLocationSource()
    let access = FakeLocationAccess()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let clock = ManualClock(start: TripFixtures.at(8, 0))
    let defaults = UserDefaults(suiteName: "TripSessionTests-\(UUID().uuidString)")!

    func session() -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store, locations: locations,
                    access: access, makeAPI: { _, _ in api }, now: { clock.now })
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
    /// A session whose sign-out deadline the test fires by hand.
    func session(deadline: ManualSleep) -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store, locations: locations,
                    access: access, makeAPI: { _, _ in api }, now: { clock.now }, sleep: deadline.sleep(for:))
    }

    func pausedSession(trip: TripGeometry = TripFixtures.t1) throws -> (session: TripSession, active: ActiveTrip) {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-60)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        defaults.set("d@test.com", forKey: "email")
        let active = ActiveTrip(serverTripID: 7, vehicle: bus, trip: trip,
                                startedAt: clock.now.addingTimeInterval(-600), driverEmail: "d@test.com")
        try store.save(active)
        return (session(), active)
    }

    /// Before you start asks through the same object the start checks, so
    /// the two can't disagree about what was allowed.
    @Test func theLocationAccessIsTheOneTheStartChecks() {
        #expect(session().locationAccess as? FakeLocationAccess === access)
    }

    @Test func startsSignedOutWithoutAToken() {
        #expect(session().phase == .signedOut)
    }

    @Test func anExpiredTokenWithNothingToRenewItMeansSignedOut() throws {
        try tokens.save(StoredToken(token: "old", issuedAt: clock.now.addingTimeInterval(-3600),
                                    expiresAt: clock.now.addingTimeInterval(-2700)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        #expect(session().phase == .signedOut)
    }

    @Test func anExpiredTokenWithARefreshTokenSkipsLogin() throws {
        try tokens.save(StoredToken(token: "old", issuedAt: clock.now.addingTimeInterval(-3600),
                                    refreshToken: "refresh-1", expiresAt: clock.now.addingTimeInterval(-2700)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        #expect(session().phase == .idle)
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
        #expect(try tokens.load() == StoredToken(token: "jwt", issuedAt: clock.now, refreshToken: "refresh-1",
                                                 expiresAt: clock.now.addingTimeInterval(900)))
        #expect(s.settings.serverURLString == "https://positions.example.org")
        #expect(s.settings.email == "d@test.com")
        #expect(s.settings.signInServerURL == server)
    }

    @Test func signInFailureStaysSignedOut() async {
        api.loginError = APIError.status(401, message: "invalid credentials")
        let s = session()
        await #expect(throws: APIError.status(401, message: "invalid credentials")) {
            try await s.signIn(serverURL: server, email: "d@test.com", password: "bad")
        }
        #expect(s.phase == .signedOut)
    }

    /// The token and any trip belong to the server that issued them, so a
    /// relaunch and a re-login stay there after the agency names another.
    @Test func aRelaunchAndAReloginKeepTheSavedServerWhenTheAgencyNamesAnother() async throws {
        try tokens.save(StoredToken(token: "jwt", issuedAt: clock.now.addingTimeInterval(-3600)))
        defaults.set("https://positions.example.org", forKey: "serverURL")
        defaults.set([AppSettings.managedServerURLKey: "https://other.example.org"],
                     forKey: AppSettings.managedConfigurationKey)
        var servers: [URL] = []
        let s = TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store, locations: locations,
                            access: access, makeAPI: { url, _ in servers.append(url); return api }, now: { clock.now })
        try await s.reauthenticate(password: "pw")
        #expect(s.phase == .idle)
        #expect(servers == [server, server])
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
        #expect(s.settings.recentVehicleIDs == ["bus-1"])
    }

    /// The vehicle joins Recent only once the server has started a trip on it.
    @Test func startRemembersTheVehicleOnlyOnceTheServerHasStartedTheTrip() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        api.startError = APIError.status(403, message: "driver is not assigned to this vehicle")
        await #expect(throws: APIError.status(403, message: "driver is not assigned to this vehicle")) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(s.settings.recentVehicleIDs.isEmpty)

        api.startError = nil
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(s.settings.recentVehicleIDs == ["bus-1"])
        #expect(AppSettings(defaults: defaults).recentVehicleIDs == ["bus-1"], "a relaunch finds it")
    }

    /// Favourites and recent vehicles belong to the phone, not the account.
    @Test func signOutKeepsTheFavoritesAndRecentVehicles() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        s.settings.toggleFavorite("4317")
        try await s.start(vehicle: bus, tripID: "T1")
        try await s.end()

        await s.signOut()
        #expect(s.phase == .signedOut)
        #expect(s.settings.favoriteVehicleIDs == ["4317"])
        #expect(s.settings.recentVehicleIDs == ["bus-1"])
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

    /// The server must not hear of a trip the phone cannot report: the
    /// geometry fetch counts too, so a refused start makes no request at all,
    /// not even the end of a trip this driver ended on this phone only.
    @Test(arguments: [
        (LocationAccessState.denied, TripSessionError.locationDenied),
        (.restricted, .locationDenied),
        (.servicesOff, .locationServicesOff),
    ])
    func aRefusedLocationStopsTheStartBeforeAnyServerCall(state: LocationAccessState, refusal: TripSessionError) async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        access.state = state
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        await #expect(throws: refusal) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(api.ends.isEmpty, "the pending end must not have been sent")
        #expect(try store.loadPendingEnds() == [owed], "kept for the next start")
        #expect(api.tripRequests.isEmpty, "the geometry must not have been fetched")
        #expect(api.starts.isEmpty, "the server trip must not have been started")
        #expect(s.phase == .idle)
        #expect(try store.load() == nil)
        #expect(locations.handles.isEmpty)
        #expect(access.requests == 0, "a driver who already answered is not asked again")
        #expect(access.preciseRequests == 0)
    }

    @Test func anAllowedLocationStartsWithoutAPrompt() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(access.requests == 0)
        #expect(access.preciseRequests == 0)
        #expect(api.starts.count == 1)
    }

    @Test func aDriverNotAskedYetIsAskedOnceAndTheStartGoesAhead() async throws {
        access.state = .notDetermined
        access.requestAnswer = .allowed
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(access.requests == 1)
        #expect(access.preciseRequests == 0)
        #expect(api.starts.count == 1)
        if case .active = s.phase {} else { Issue.record("expected active") }
    }

    @Test func refusingThePromptStopsTheStartBeforeAnyServerCall() async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        access.state = .notDetermined
        access.requestAnswer = .denied
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        await #expect(throws: TripSessionError.locationDenied) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(access.requests == 1)
        #expect(api.ends.isEmpty)
        #expect(try store.loadPendingEnds() == [owed])
        #expect(api.tripRequests.isEmpty)
        #expect(api.starts.isEmpty)
        #expect(s.phase == .idle)
    }

    /// The prompt's wait ran out with the driver still not asked, as on a
    /// start from the car with the phone app in the background. Nothing is
    /// off in Settings, so the refusal must not say it is.
    @Test func aPromptThatTimedOutRefusesAsNotAskedNotDenied() async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        access.state = .notDetermined
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        await #expect(throws: TripSessionError.locationNotAsked) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(access.requests == 1)
        #expect(api.ends.isEmpty)
        #expect(try store.loadPendingEnds() == [owed])
        #expect(api.tripRequests.isEmpty)
        #expect(api.starts.isEmpty)
        #expect(s.phase == .idle)
    }

    @Test func approximateLocationAsksOnceForPreciseAndTheStartGoesAhead() async throws {
        access.state = .approximateOnly
        access.preciseAnswer = .allowed
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(access.preciseRequests == 1)
        #expect(access.requests == 0)
        #expect(api.starts.count == 1)
    }

    @Test func keepingApproximateLocationStopsTheStartBeforeAnyServerCall() async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        access.state = .approximateOnly
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        await #expect(throws: TripSessionError.preciseLocationOff) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(access.preciseRequests == 1)
        #expect(api.ends.isEmpty)
        #expect(try store.loadPendingEnds() == [owed])
        #expect(api.tripRequests.isEmpty)
        #expect(api.starts.isEmpty)
        #expect(s.phase == .idle)
    }

    /// The first prompt has its own Precise switch; a driver who turns it off
    /// there is asked for precise location once before the start is refused.
    @Test func allowingWithoutPreciseAtThePromptAsksForPreciseOnce() async throws {
        access.state = .notDetermined
        access.requestAnswer = .approximateOnly
        access.preciseAnswer = .allowed
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(access.requests == 1)
        #expect(access.preciseRequests == 1)
        #expect(api.starts.count == 1)
    }

    @Test func theSessionStaysStartingWhileThePromptIsUp() async throws {
        access.state = .notDetermined
        access.requestAnswer = .allowed
        access.holdsRequest = true
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        let starting = Task { try await s.start(vehicle: bus, tripID: "T1") }
        #expect(await eventually { access.isHoldingRequest }, "expected the prompt to be up")
        #expect(s.phase == .starting, "the run lists stay disabled while the driver decides")
        await #expect(throws: TripSessionError.busy) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(api.tripRequests.isEmpty)

        access.answer()
        try await starting.value
        #expect(access.requests == 1)
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

    /// P44: Resume carries the sent count on from before the relaunch.
    @Test func theSentCountCarriesOnAfterARelaunch() async throws {
        let (s, active) = try pausedSession()
        try store.saveSent(142, for: active.serverTripID)
        s.resume()
        #expect(s.fixesSent == 142)
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.fixesSent == 143 })
        #expect(store.loadSent(for: active.serverTripID) == 143)
    }

    @Test func aFreshTripCountsFromZeroKeepsEachReportAndDropsTheCountWhenItEnds() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        // A count kept for an earlier trip is not this one's.
        try store.saveSent(99, for: api.startedID - 1)
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(s.fixesSent == 0)
        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.fixesSent == 1 })
        #expect(store.loadSent(for: api.startedID) == 1)
        clock.advance(6)
        locations.emitFix(lat: 47.6050, lon: -122.3300, at: TripFixtures.at(8, 5, 6))
        #expect(await eventually { s.fixesSent == 2 })
        #expect(store.loadSent(for: api.startedID) == 2)

        try await s.end()
        #expect(store.loadSent(for: api.startedID) == 0)
    }

    @Test func anOffRouteFixKeepsTheLastOnRouteHint() async throws {
        // The evaluator already ignores an off-route `previous`
        // (`offRoutePreviousDoesNotHint`). What this pins is what the session
        // hands it: storing the off-route judgement as the hint would erase the
        // one before it, and on a loop the next fix would then snap to the wrong
        // pass. A straight shape cannot show this, so the trip is the loop, whose
        // shared start/end point is told apart by the hint alone.
        api.geometry = TripFixtures.loop
        let shapeLength = AdherenceEvaluator(trip: TripFixtures.loop)!.shape.length
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "L")

        locations.emitFix(lat: 47.6001, lon: -122.3292, at: TripFixtures.at(9, 15))
        #expect(await eventually { s.latest != nil })
        #expect(s.latest?.isOnRoute == true)

        // A detour 112 m west of the first leg. Off route, and its own
        // projection is back on the first pass: the position the next fix must
        // not be judged against.
        clock.advance(120)
        locations.emitFix(lat: 47.6040, lon: -122.3315, at: TripFixtures.at(9, 17))
        #expect(await eventually { s.latest?.isOnRoute == false })
        #expect(s.latest?.projection.alongShape ?? 0 < 600)

        clock.advance(180)
        locations.emitFix(lat: 47.6000, lon: -122.3300, at: TripFixtures.at(9, 20))
        #expect(await eventually { s.latest?.fix.timestamp == TripFixtures.at(9, 20) })
        #expect(s.latest?.projection.alongShape ?? 0 > shapeLength - 60,
                "the hint from before the detour still picks the last pass")
        // On the first pass the bus would read 20 minutes late for the rest of
        // the trip.
        #expect(s.latest?.status == .onTime)
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

    /// Resume's End trip: a failed end leaves the trip as it was, paused, so
    /// the screen stays on Resume with the banner.
    @Test func aFailedEndOfAPausedTripLeavesItPaused() async throws {
        api.endError = APIError.transport("offline")
        let (s, active) = try pausedSession()
        await #expect(throws: APIError.transport("offline")) { try await s.end() }
        #expect(s.phase == .paused(active))
        #expect(try store.load() == active)
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
        // The second is the End locally's own end, sent before the new start.
        #expect(api.ends == [42, 42], "only the trip the call was given was ended on the server")
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

    /// A 404 means the server has no open trip under that id: an earlier end
    /// got through and only its reply was lost, or a dispatcher ended it.
    @Test func endTreatsA404AsAlreadyEnded() async throws {
        api.endError = APIError.status(404, message: "active trip not found")
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        try await s.end()
        #expect(api.ends == [42])
        #expect(s.phase == .idle)
        #expect(try store.load() == nil)
        #expect(locations.handles[0].invalidated)
    }

    /// Only a 404 means the trip is already ended; any other status is a
    /// failure the driver has to see, so a new server error is not mistaken
    /// for success.
    @Test func endTreatsOtherStatusesAsFailures() async throws {
        api.endError = APIError.status(500, message: "")
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        await #expect(throws: APIError.status(500, message: "")) { try await s.end() }
        if case .active = s.phase {} else { Issue.record("a failed end keeps the trip active") }
        #expect(try store.load() != nil)
        #expect(!locations.handles[0].invalidated)
    }

    /// The pending end belongs to the driver who started the trip, not to
    /// whoever is signed in when it is ended locally: only the starter's next
    /// start may send it.
    @Test func endLocallyRemembersTheEndForTheDriverWhoStartedTheTrip() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "a@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        s.settings.email = "b@test.com"

        s.endLocally()
        #expect(try store.loadPendingEnds() == [PendingTripEnd(serverTripID: 42, driverEmail: "a@test.com", serverURL: server.absoluteString)])
        #expect(try store.load() == nil)
        #expect(s.phase == .idle)
        #expect(api.ends.isEmpty, "ending locally does not call the server")
    }

    /// The server still has the trip open and would refuse the start with
    /// 409, so its end goes first.
    @Test func theNextStartSendsTheRememberedEndFirst() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        s.endLocally()

        api.startedID = 43
        try await s.start(vehicle: bus, tripID: "T1")
        #expect(api.calls == ["trip T1", "start T1", "end 42", "trip T1", "start T1"])
        #expect(try store.loadPendingEnds().isEmpty)
        #expect(s.activeTrip?.serverTripID == 43)
    }

    /// A 404 means the server has no open trip under that id, which is what
    /// the end was for.
    @Test func aPendingEndAnswered404CountsAsSent() async throws {
        try store.savePendingEnds([PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)])
        api.endError = APIError.status(404, message: "active trip not found")
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        try await s.start(vehicle: bus, tripID: "T1")
        #expect(api.ends == [7])
        #expect(try store.loadPendingEnds().isEmpty)
        #expect(api.starts.count == 1)
        #expect(s.activeTrip?.serverTripID == 42)
    }

    /// The server would refuse the start while the old trip is open, so it
    /// is not asked for anything else: no trip fetched, no trip started.
    @Test(arguments: [APIError.transport("offline"), APIError.status(500, message: "")])
    func aPendingEndThatCannotBeSentRefusesTheStart(_ failure: APIError) async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        api.endError = failure
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        await #expect(throws: TripSessionError.previousTripNotEnded) {
            try await s.start(vehicle: bus, tripID: "T1")
        }
        #expect(api.ends == [7])
        #expect(api.tripRequests.isEmpty)
        #expect(api.starts.isEmpty)
        #expect(try store.loadPendingEnds() == [owed], "kept for the next start")
        #expect(s.phase == .idle)
    }

    /// Another driver's open trip does not block this driver's start, and
    /// their pending end waits for their own next start. The email matches
    /// case-insensitively, as it does for a stored trip.
    @Test func onlyThisDriversPendingEndsAreSent() async throws {
        let theirs = PendingTripEnd(serverTripID: 7, driverEmail: "a@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([theirs, PendingTripEnd(serverTripID: 8, driverEmail: "B@Test.com", serverURL: server.absoluteString)])
        let s = session()
        try await s.signIn(serverURL: server, email: "b@test.com", password: "pw")

        try await s.start(vehicle: bus, tripID: "T1")
        #expect(api.ends == [8])
        #expect(try store.loadPendingEnds() == [theirs])
        #expect(s.activeTrip?.serverTripID == 42)
    }

    /// A trip ended locally twice (its record outlived the first time, as it
    /// does if clearing it fails) is still one pending end.
    @Test func endingTheSameTripLocallyTwiceRemembersItOnce() throws {
        let (first, active) = try pausedSession()
        first.endLocally()
        try store.save(active)
        let second = session()
        #expect(second.phase == .paused(active))

        second.endLocally()
        #expect(try store.loadPendingEnds() == [PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)])
    }

    /// The driver asked to stop; a disk that cannot hold the pending end does
    /// not keep them on the trip.
    @Test func endLocallyStopsEvenIfTheEndCannotBeRemembered() async throws {
        let unwritable = UnwritablePendingEndsStore()
        let s = TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: unwritable,
                            locations: locations, access: access, makeAPI: { _, _ in api }, now: { clock.now })
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        s.endLocally()
        #expect(s.phase == .idle)
        #expect(try unwritable.load() == nil)
        #expect(locations.handles[0].invalidated)
    }

    /// Trip ids are numbered per server, so an end owed to another server is
    /// not sent here, where the same id could be another trip; it waits for
    /// the driver to sign in there again. A retyped address is still this
    /// server, or its end would be held back and the start refused.
    @Test func aPendingEndOwedToAnotherServerIsLeftForIt() async throws {
        let elsewhere = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: "https://other.example.org")
        try store.savePendingEnds([elsewhere, PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com",
                                                             serverURL: "https://Positions.example.org/")])
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        try await s.start(vehicle: bus, tripID: "T1")
        #expect(api.ends == [7])
        #expect(try store.loadPendingEnds() == [elsewhere])
        #expect(s.activeTrip?.serverTripID == 42)
    }

    @Test func theSameTripIDOnAnotherServerIsAnotherPendingEnd() throws {
        let elsewhere = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: "https://other.example.org")
        try store.savePendingEnds([elsewhere])
        let (s, _) = try pausedSession()

        s.endLocally()
        #expect(try store.loadPendingEnds() == [
            elsewhere, PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString),
        ])
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
        // The same TripDescriptor fields Android sends.
        #expect(api.posted[0].routeID == TripFixtures.t1.routeID)
        #expect(api.posted[0].startDate == TripFixtures.t1.serviceDate)

        try await s.end()
        #expect(api.ends == [7])
    }

    /// Spec §8: a fix the phone cannot trust is no position at all. It must
    /// not move the map and must not be reported, and the driver has to be
    /// told rather than left looking at a stale bus that reads as live.
    @Test func accuracyLimitedFreezesAdherenceAndNamesPreciseLocation() async throws {
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
        #expect(await eventually { s.reporting == .preciseLocationOff })
        #expect(s.latest == good, "the map holds the last fix it could trust")
        await Task.yield()
        #expect(api.posted.count == 1, "an accuracy-limited fix is not reported")

        clock.advance(10)
        locations.emitFix(lat: 47.6065, lon: -122.3300, at: TripFixtures.at(8, 5, 20))
        #expect(await eventually { s.fixesSent == 2 })
        #expect(s.reporting == .connected)
        #expect(s.latest?.fix.latitude == 47.6065)
    }

    /// A refused location is not a lost signal: the driver can turn it back on
    /// in Settings, and the banner has to say so.
    @Test func locationTurnedOffDuringATripIsNamedNotNoGPS() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        locations.emit(LocationSample(fix: nil, diagnostic: .authorizationDenied))
        #expect(await eventually { s.reporting == .locationDenied })

        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        #expect(await eventually { s.reporting == .connected }, "a good fix means access is back")
        #expect(await eventually { api.posted.count == 1 })
    }

    /// A new stream starts clean, as it does for the signal: if access is
    /// still off, that stream says so again. An ended trip shows nothing.
    @Test func resumingOrEndingClearsALocationAccessProblem() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        locations.emit(LocationSample(fix: nil, diagnostic: .authorizationDenied))
        #expect(await eventually { s.reporting == .locationDenied })
        locations.finish(throwing: APIError.transport("dropped"))
        #expect(await eventually { s.reporting == .locationLost })
        s.resume()
        #expect(s.reporting == .connected)

        locations.emit(LocationSample(fix: nil, diagnostic: .authorizationDenied))
        #expect(await eventually { s.reporting == .locationDenied })
        try await s.end()
        #expect(s.reporting == .connected)
    }

    /// Under signed out, most serious first: a dead stream, then the app
    /// needing the foreground, then location access, then the signal. Each
    /// step changes what the screen says, so each one is known to have been
    /// handled.
    @Test func locationAccessRanksBetweenTheForegroundAndTheSignal() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        locations.emit(LocationSample(fix: nil, diagnostic: .locationUnavailable))
        #expect(await eventually { s.reporting == .noGPS })

        locations.emit(LocationSample(fix: nil, diagnostic: .authorizationDenied))
        #expect(await eventually { s.reporting == .locationDenied }, "access outranks the signal")

        locations.emit(LocationSample(fix: nil, diagnostic: .insufficientlyInUse))
        #expect(await eventually { s.reporting == .needsForeground }, "the foreground outranks access")

        locations.finish(throwing: APIError.transport("dropped"))
        #expect(await eventually { s.reporting == .locationLost }, "a dead stream outranks the rest")
    }

    /// Signing in comes before Resume: a driver who is signed out and has
    /// also lost the location stream sees Sign in, and Location stopped once
    /// signed in again. Here the 401 lands after the stream has gone.
    @Test func signedOutOutranksALostStream() async throws {
        api.postError = APIError.status(401, message: "invalid token")
        api.postDelay = .milliseconds(500)
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")

        locations.emitFix(lat: 47.6045, lon: -122.3300, at: TripFixtures.at(8, 5))
        locations.finish(throwing: APIError.transport("dropped"))
        #expect(await eventually { s.reporting == .locationLost }, "the stream is gone before the server answers")
        #expect(await eventually { s.reporting == .authExpired }, "signed out outranks the lost stream")

        api.postError = nil
        try await s.reauthenticate(password: "pw")
        #expect(s.reporting == .locationLost, "signed in again, the lost stream shows with its Resume")
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
        await s.signOut()
        #expect(s.phase == .signedOut)

        s.resume()
        #expect(s.phase == .signedOut)
        #expect(locations.handles.isEmpty, "nothing was subscribed to")
    }

    /// The client reads the token from the store for every request, so the
    /// logout has to go out before the store is cleared.
    @Test func signOutLogsOutOnTheServerWhileTheTokenIsStillStored() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.loadVehicles()
        var storedDuringLogout: StoredToken?
        var phaseDuringLogout: TripSession.Phase?
        var signingOutDuringLogout = false
        api.whileLoggingOut = {
            storedDuringLogout = try? tokens.load()
            phaseDuringLogout = s.phase
            signingOutDuringLogout = s.isSigningOut
        }

        await s.signOut()
        #expect(api.logouts == 1)
        #expect(storedDuringLogout != nil, "the logout went out with the token")
        #expect(phaseDuringLogout == .idle)
        #expect(signingOutDuringLogout)
        #expect(!s.isSigningOut)
        #expect(try tokens.load() == nil)
        #expect(s.vehicles.isEmpty)
        #expect(s.phase == .signedOut)
        await #expect(throws: TripSessionError.notSignedIn) { try await s.loadVehicles() }
    }

    /// A driver handing the phone on must never be left signed in, whatever
    /// the server said.
    @Test(arguments: [
        APIError.transport("The Internet connection appears to be offline."),
        APIError.status(500, message: "internal server error"),
        APIError.status(401, message: "invalid token"),
    ])
    func aLogoutThatFailsStillSignsOut(error: APIError) async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.loadVehicles()
        api.logoutError = error

        await s.signOut()
        #expect(api.logouts == 1)
        #expect(try tokens.load() == nil)
        #expect(s.vehicles.isEmpty)
        #expect(s.phase == .signedOut)
        await #expect(throws: TripSessionError.notSignedIn) { try await s.loadVehicles() }
    }

    /// A running trip reports with this sign-in, so the driver ends the trip
    /// before signing out.
    @Test func signOutDuringATripDoesNothing() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        try await s.start(vehicle: bus, tripID: "T1")
        let during = s.phase
        try #require(s.activeTrip != nil)

        await s.signOut()
        #expect(api.logouts == 0)
        #expect(try tokens.load() != nil)
        #expect(s.phase == during)
    }

    /// The client has already tried a refresh, so a 401 on the pickers means
    /// the server ended the session: it is ended here without a logout.
    @Test func a401OnThePickersEndsTheSessionWithoutALogout() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        #expect(s.handleIfUnauthorized(APIError.status(401, message: "invalid token")))
        #expect(try tokens.load() == nil)
        #expect(s.phase == .signedOut)
        await Task.yield()
        #expect(api.logouts == 0)
    }

    /// The phone stays with the agency and goes to the next driver: the
    /// server stays filled in, the email does not.
    @Test func signOutForgetsTheEmailButKeepsTheServer() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        await s.signOut()
        #expect(s.settings.email == "")
        #expect(s.settings.signInServerURL == server)
        #expect(AppSettings(defaults: defaults).email == "", "a relaunch finds it gone too")
    }

    /// A 401 ends this driver's session, not their turn with the phone: the
    /// sign-in screen still has their email.
    @Test func a401KeepsTheEmailForSigningBackIn() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        s.handleIfUnauthorized(APIError.status(401, message: "invalid token"))
        #expect(s.phase == .signedOut)
        #expect(s.settings.email == "d@test.com")
    }

    /// A start from the car can come while the logout is out. It is refused:
    /// the logout is revoking the token the trip would report with.
    @Test func aStartWhileTheLogoutIsOutIsRefused() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        var startError: (any Error)?
        api.whileLoggingOut = {
            do { try await s.start(vehicle: bus, tripID: "T1") } catch { startError = error }
        }

        await s.signOut()
        #expect(startError as? TripSessionError == .notSignedIn)
        #expect(api.calls == ["logout"], "the server heard nothing of the start")
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(s.settings.email == "")
    }

    /// The same for a paused trip resumed while the logout is out.
    @Test func aResumeWhileTheLogoutIsOutIsRefused() async throws {
        let (s, active) = try pausedSession()
        api.whileLoggingOut = { s.resume() }

        await s.signOut()
        #expect(locations.handles.isEmpty, "nothing was subscribed to")
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(try store.load() == active)
    }

    @Test func aSecondSignOutWhileTheFirstIsOutSendsNoSecondLogout() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        var signingOutAfterTheSecond = false
        api.whileLoggingOut = {
            api.whileLoggingOut = nil
            await s.signOut()
            signingOutAfterTheSecond = s.isSigningOut
        }

        await s.signOut()
        #expect(api.logouts == 1)
        #expect(signingOutAfterTheSecond, "the second call did not end the first one's sign-out")
        #expect(s.phase == .signedOut)
    }

    /// A 401 while the logout is out ends the session on the phone and shows
    /// Sign in, so the next driver can sign in before the logout returns. That
    /// logout was the first driver's: it must not clear the second's session.
    @Test func aSignInWhileTheLogoutIsOutIsKeptWhenTheLogoutReturns() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "first@test.com", password: "pw")
        var secondSignInError: (any Error)?
        var signingOutAfterTheSecondSignIn = true
        api.whileLoggingOut = {
            s.handleIfUnauthorized(APIError.status(401, message: "invalid token"))
            api.tokens = AuthTokens(accessToken: "jwt-2", refreshToken: "refresh-2", expiresIn: 900)
            do {
                try await s.signIn(serverURL: server, email: "second@test.com", password: "pw")
            } catch {
                secondSignInError = error
            }
            signingOutAfterTheSecondSignIn = s.isSigningOut
        }

        await s.signOut()
        #expect(secondSignInError == nil)
        #expect(api.logouts == 1)
        #expect(!signingOutAfterTheSecondSignIn, "the second driver can start a trip at once")
        #expect(s.phase == .idle)
        #expect(try tokens.load()?.token == "jwt-2")
        #expect(s.settings.email == "second@test.com")
        #expect(!s.isSigningOut)
        try await s.loadVehicles()
    }

    /// The second driver can sign out too while the first logout is still
    /// out. Theirs is a new sign-out with its own logout, and the first one
    /// returning must not end it early: a start from the car would then slip
    /// through while the second logout is revoking the token.
    @Test func theFirstLogoutReturningDoesNotEndASecondDriversSignOut() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "first@test.com", password: "pw")
        let (firstReturned, release) = AsyncStream.makeStream(of: Void.self)
        var second: Task<Void, Never>?
        var secondSignInError: (any Error)?
        var secondWasOutWhenTheFirstReturned = false
        var signingOutWhenTheFirstReturned = false
        api.whileLoggingOut = {
            s.handleIfUnauthorized(APIError.status(401, message: "invalid token"))
            do {
                try await s.signIn(serverURL: server, email: "second@test.com", password: "pw")
            } catch {
                secondSignInError = error
            }
            api.whileLoggingOut = {
                secondWasOutWhenTheFirstReturned = true
                for await _ in firstReturned { break }
                signingOutWhenTheFirstReturned = s.isSigningOut
            }
            second = Task { await s.signOut() }
            // Lets the second sign-out reach its logout before the first returns.
            await Task.yield()
        }

        await s.signOut()
        #expect(secondSignInError == nil)
        #expect(secondWasOutWhenTheFirstReturned)
        release.yield(())
        await second?.value
        #expect(signingOutWhenTheFirstReturned, "the first logout's return did not end the second sign-out")
        #expect(api.logouts == 2)
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(s.settings.email == "")
        #expect(!s.isSigningOut)
    }

    /// Sign-out is the last moment this driver's token works, so the trips
    /// they ended on this phone are ended on the server first. Another
    /// driver's end waits for that driver.
    @Test func signOutSendsThisDriversPendingEndsBeforeTheLogout() async throws {
        let theirs = PendingTripEnd(serverTripID: 8, driverEmail: "a@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString), theirs])
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        await s.signOut()
        #expect(api.calls == ["end 7", "logout"])
        #expect(try store.loadPendingEnds() == [theirs])
        #expect(s.phase == .signedOut)
        #expect(s.settings.email == "")
    }

    /// An end the server doesn't take is kept for this driver's next start,
    /// and the sign-out goes on: the logout, then the phone.
    @Test(arguments: [APIError.transport("offline"), APIError.status(500, message: "")])
    func aPendingEndThatFailsAtSignOutIsKeptAndTheSignOutGoesOn(_ failure: APIError) async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        api.endError = failure
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        await s.signOut()
        #expect(api.calls == ["end 7", "logout"])
        #expect(try store.loadPendingEnds() == [owed], "kept for the next start")
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
    }

    /// A sign-out sends what this driver owes this server and nothing else,
    /// as a start does: another driver's end waits for that driver, and an
    /// end owed to another server for a sign-in there.
    @Test func aSignOutSendsNoOtherDriversOrServersEnds() async throws {
        let theirs = PendingTripEnd(serverTripID: 7, driverEmail: "a@test.com", serverURL: server.absoluteString)
        let elsewhere = PendingTripEnd(serverTripID: 8, driverEmail: "d@test.com", serverURL: "https://other.example.org")
        try store.savePendingEnds([theirs, elsewhere])
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        await s.signOut()
        #expect(api.calls == ["logout"])
        #expect(try store.loadPendingEnds() == [theirs, elsewhere])
    }

    /// A server that takes the end and never answers holds the sign-out for
    /// `signOutWait` at most. Then the end is cancelled and no logout goes
    /// out, and the phone signs out. The end stays stored: a cancelled end
    /// wasn't sent.
    @Test(.timeLimit(.minutes(1)))
    func aServerThatDoesNotAnswerHoldsTheSignOutForTenSecondsAtMost() async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        let deadline = ManualSleep()
        let s = session(deadline: deadline)
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        var endIsOut = false
        api.whileEnding = {
            endIsOut = true
            // Throws when cancelled, as a cancelled request does.
            try await Task.sleep(for: .seconds(3600))
        }

        let signingOut = Task { await s.signOut() }
        try #require(await eventually { endIsOut })
        deadline.fire()
        try #require(await eventually { !s.isSigningOut }, "the sign-out returned")
        await signingOut.value
        #expect(deadline.asked == [.seconds(10)])
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(s.settings.email == "")
        #expect(api.logouts == 0)
        #expect(try store.loadPendingEnds() == [owed], "a cancelled end was not sent")
    }

    /// The same limit covers the logout: the ends and the logout share it,
    /// so one that never answers is cancelled there too.
    @Test(.timeLimit(.minutes(1)))
    func aLogoutThatDoesNotAnswerIsCancelledAtTheSameDeadline() async throws {
        try store.savePendingEnds([PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)])
        let deadline = ManualSleep()
        let s = session(deadline: deadline)
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        var logoutIsOut = false
        api.whileLoggingOut = {
            logoutIsOut = true
            try await Task.sleep(for: .seconds(3600))
        }

        let signingOut = Task { await s.signOut() }
        try #require(await eventually { logoutIsOut })
        deadline.fire()
        try #require(await eventually { !s.isSigningOut }, "the sign-out returned")
        await signingOut.value
        #expect(deadline.asked == [.seconds(10)], "one limit for both")
        #expect(api.calls == ["end 7"])
        #expect(try store.loadPendingEnds().isEmpty)
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
    }

    /// A 401 during the ends shows Sign in, and the next driver can sign in
    /// before the sign-out goes on. No logout may go out then: the client
    /// sends the stored token, which is the new driver's by now, so it would
    /// end their session on the server. The first driver's session is over
    /// already; the 401 said so.
    @Test func aSignInDuringThePendingEndsIsNotLoggedOut() async throws {
        let owed = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: server.absoluteString)
        try store.savePendingEnds([owed])
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        var secondSignInError: (any Error)?
        api.whileEnding = {
            s.handleIfUnauthorized(APIError.status(401, message: "invalid token"))
            api.tokens = AuthTokens(accessToken: "jwt-2", refreshToken: "refresh-2", expiresIn: 900)
            do {
                try await s.signIn(serverURL: server, email: "second@test.com", password: "pw")
            } catch {
                secondSignInError = error
            }
            // The end went out with the first driver's token, which is refused.
            api.endError = APIError.status(401, message: "invalid token")
        }

        await s.signOut()
        #expect(secondSignInError == nil)
        #expect(api.logouts == 0, "a logout now would have gone out with jwt-2")
        #expect(try tokens.load()?.token == "jwt-2")
        #expect(s.settings.email == "second@test.com")
        #expect(s.phase == .idle)
        #expect(!s.isSigningOut)
        #expect(try store.loadPendingEnds() == [owed])
    }

    /// The limit is not a wait: a server that answers at once signs out at
    /// once, with the real 10 s sleep behind it.
    @Test func aQuickSignOutDoesNotWaitForTheDeadline() async throws {
        let s = session()
        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")

        let started = ContinuousClock.now
        await s.signOut()
        #expect(ContinuousClock.now - started < .seconds(2))
        #expect(s.phase == .signedOut)
    }

    @Test func signOutFromPausedKeepsTheStoredTripForNextSignIn() async throws {
        let (s, active) = try pausedSession()
        #expect(s.phase == .paused(active))

        await s.signOut()
        #expect(api.logouts == 1)
        #expect(s.phase == .signedOut)
        #expect(try tokens.load() == nil)
        #expect(try store.load() == active, "the stored trip survives a sign-out")

        try await s.signIn(serverURL: server, email: "d@test.com", password: "pw")
        #expect(s.phase == .paused(active))
    }
}

/// Holds the active trip but cannot write pending ends, as a full disk would.
nonisolated final class UnwritablePendingEndsStore: ActiveTripStoring, @unchecked Sendable {
    private let trips = InMemoryActiveTripStore()

    func load() throws -> ActiveTrip? { try trips.load() }
    func save(_ trip: ActiveTrip) throws { try trips.save(trip) }
    func clear() throws { try trips.clear() }
    func loadSent(for serverTripID: Int64) -> Int { trips.loadSent(for: serverTripID) }
    func saveSent(_ count: Int, for serverTripID: Int64) throws { try trips.saveSent(count, for: serverTripID) }
    func loadPendingEnds() throws -> [PendingTripEnd] { [] }
    func savePendingEnds(_ ends: [PendingTripEnd]) throws { throw CocoaError(.fileWriteOutOfSpace) }
}

/// Stands in for `Task.sleep` in the sign-out's deadline: it records what it
/// was asked for and returns when the test fires it, not 10 s later.
/// Cancelled first, it throws `CancellationError`, as `Task.sleep` does.
@MainActor final class ManualSleep {
    private(set) var asked: [Duration] = []
    private let fired: AsyncStream<Void>
    private let continuation: AsyncStream<Void>.Continuation

    init() { (fired, continuation) = AsyncStream.makeStream(of: Void.self) }

    func sleep(for duration: Duration) async throws {
        asked.append(duration)
        for await _ in fired { return }
        throw CancellationError()
    }

    func fire() { continuation.yield() }
}

@Suite struct TripSessionErrorTests {
    @Test func onlyTheLocationRefusalsOpenSettings() {
        #expect(TripSessionError.locationDenied.opensSettings)
        #expect(TripSessionError.locationServicesOff.opensSettings)
        #expect(TripSessionError.preciseLocationOff.opensSettings)
        #expect(!TripSessionError.notSignedIn.opensSettings)
        #expect(!TripSessionError.unusableTrip.opensSettings)
        #expect(!TripSessionError.busy.opensSettings)
        #expect(!TripSessionError.previousTripNotEnded.opensSettings)
        #expect(!TripSessionError.locationNotAsked.opensSettings, "nothing in Settings is off")
    }

    @Test func eachLocationRefusalSaysWhatToChange() {
        #expect(TripSessionError.locationDenied.localizedDescription
            == "Location is off for OBA Vehicle Tracker. Turn it on in Settings to start a trip.")
        #expect(TripSessionError.locationNotAsked.localizedDescription
            == "Open OBA Vehicle Tracker on your iPhone to allow location.")
        #expect(TripSessionError.locationServicesOff.localizedDescription
            == "Location Services are off on this iPhone. Turn them on in Settings > Privacy & Security > Location Services.")
        #expect(TripSessionError.preciseLocationOff.localizedDescription
            == "Precise Location is off. Turn it on in Settings so the vehicle shows on the right street.")
    }
}
