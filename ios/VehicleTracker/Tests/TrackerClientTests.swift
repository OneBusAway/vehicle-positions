import Foundation
import Testing
@testable import VehicleTracker

@Suite(.serialized) struct TrackerClientTests {
    let base = URL(string: "https://positions.example.org")!

    func client(token: String? = "tok") throws -> TrackerClient {
        let tokens = InMemoryTokenStore(token.map { StoredToken(token: $0, issuedAt: Date()) })
        return try TrackerClient(baseURL: base, tokens: tokens, configuration: StubURLProtocol.configuration())
    }

    @Test func refusesAnInsecureBaseURL() {
        #expect(throws: APIError.insecureURL) {
            try TrackerClient(baseURL: URL(string: "http://positions.example.org")!, tokens: InMemoryTokenStore())
        }
    }

    @Test func loginPostsCredentialsWithoutAToken() async throws {
        StubURLProtocol.reset { _ in (200, Data(#"{"token":"jwt-1","access_token":"jwt-1","refresh_token":"r-1","expires_in":900}"#.utf8)) }
        let tokens = try await client(token: nil).login(email: "d@test.com", password: "pw")
        #expect(tokens == AuthTokens(accessToken: "jwt-1", refreshToken: "r-1", expiresIn: 900))
        let r = try #require(StubURLProtocol.last)
        #expect(r.method == "POST")
        #expect(r.url.path == "/api/v1/auth/login")
        #expect(r.headers["Authorization"] == nil)
        #expect(r.headers["Content-Type"] == "application/json")
        let body = try JSONSerialization.jsonObject(with: r.body!) as! [String: String]
        #expect(body == ["email": "d@test.com", "password": "pw"])
    }

    /// `token` is the server's deprecated copy of `access_token`; the two
    /// differ here so reading the wrong one shows.
    @Test func loginReadsTheAccessTokenNotTheDeprecatedAlias() async throws {
        StubURLProtocol.reset { _ in (200, Data(#"{"token":"alias","access_token":"a1","refresh_token":"r1","expires_in":120}"#.utf8)) }
        let tokens = try await client(token: nil).login(email: "d@test.com", password: "pw")
        #expect(tokens == AuthTokens(accessToken: "a1", refreshToken: "r1", expiresIn: 120))
    }

    @Test func vehiclesCarryTheBearerToken() async throws {
        StubURLProtocol.reset { _ in (200, Data(#"[{"id":"bus-1","label":"Bus 1","agency_tag":"A","active":true,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}]"#.utf8)) }
        let vehicles = try await client().myVehicles()
        #expect(vehicles == [Vehicle(id: "bus-1", label: "Bus 1")])
        #expect(StubURLProtocol.last?.headers["Authorization"] == "Bearer tok")
        #expect(StubURLProtocol.last?.url.path == "/api/v1/vehicles")
    }

    @Test func routesAndTripsDecode() async throws {
        StubURLProtocol.reset { req in
            if req.url!.path.hasSuffix("/trips") {
                return (200, Data(#"{"route_id":"R1","service_date":"20260902","timezone":"America/Los_Angeles","trips":[{"id":"T1","headsign":"North","direction_id":null,"starts_at":"2026-09-02T08:00:00-07:00","ends_at":"2026-09-02T08:10:00-07:00","first_stop":"Stop ST1","last_stop":"Stop ST3"}]}"#.utf8))
            }
            return (200, Data(#"{"routes":[{"id":"R1","short_name":"1","long_name":"Straight","color":"0077C0","text_color":"FFFFFF","type":3}]}"#.utf8))
        }
        let routes = try await client().routes()
        #expect(routes == [RouteInfo(id: "R1", shortName: "1", longName: "Straight", color: "0077C0", textColor: "FFFFFF", type: 3)])
        let page = try await client().trips(routeID: "R1")
        #expect(StubURLProtocol.last?.url.path == "/api/v1/gtfs/routes/R1/trips")
        #expect(page.trips.count == 1)
        #expect(page.trips[0].directionID == nil)
        #expect(page.trips[0].startsAt == TripFixtures.at(8, 0))
    }

    @Test func tripFetchesTheGeometry() async throws {
        StubURLProtocol.reset { _ in (200, Data(TripFixtures.tripJSON.utf8)) }
        let trip = try await client().trip(id: "T1")
        #expect(trip.id == "T1")
        #expect(StubURLProtocol.last?.url.path == "/api/v1/gtfs/trips/T1")
    }

    @Test func startTripReturnsTheServerID() async throws {
        StubURLProtocol.reset { _ in (201, Data(#"{"id":42,"user_id":1,"vehicle_id":"bus-1","route_id":"R1","gtfs_trip_id":"T1","start_time":"2026-09-02T15:00:00Z","status":"active"}"#.utf8)) }
        let id = try await client().startTrip(vehicleID: "bus-1", routeID: "R1", gtfsTripID: "T1")
        #expect(id == 42)
        let body = try JSONSerialization.jsonObject(with: StubURLProtocol.last!.body!) as! [String: String]
        #expect(body == ["vehicle_id": "bus-1", "route_id": "R1", "gtfs_trip_id": "T1"])
    }

    @Test func logoutPostsWithTheTokenAndNoBody() async throws {
        StubURLProtocol.reset { _ in (204, Data()) }
        try await client().logout()
        #expect(StubURLProtocol.count == 1)
        let sent = try #require(StubURLProtocol.last)
        #expect(sent.method == "POST")
        #expect(sent.url.path == "/api/v1/auth/logout")
        #expect(sent.headers["Authorization"] == "Bearer tok")
        #expect(sent.body == nil)
    }

    @Test func endTripPostsTheID() async throws {
        StubURLProtocol.reset { _ in (200, Data(#"{"status":"trip ended"}"#.utf8)) }
        try await client().endTrip(id: 42)
        #expect(StubURLProtocol.last?.url.path == "/api/v1/trips/end")
        let body = try JSONSerialization.jsonObject(with: StubURLProtocol.last!.body!) as! [String: Int]
        #expect(body == ["trip_id": 42])
    }

    @Test func locationReportOmitsAbsentFields() async throws {
        StubURLProtocol.reset { _ in (201, Data(#"{"status":"ok"}"#.utf8)) }
        let report = LocationReport(vehicleID: "bus-1", tripID: "T1", latitude: 47.6, longitude: -122.33,
                                    bearing: nil, speed: 8, accuracy: 5, timestamp: 1_756_800_000)
        try await client().postLocation(report)
        #expect(StubURLProtocol.last?.url.path == "/api/v1/locations")
        let body = try JSONSerialization.jsonObject(with: StubURLProtocol.last!.body!) as! [String: Any]
        #expect(Set(body.keys) == ["vehicle_id", "trip_id", "latitude", "longitude", "speed", "accuracy", "timestamp"])
        #expect(body["timestamp"] as? Int == 1_756_800_000)
    }

    @Test func locationReportCarriesRouteAndStartDate() async throws {
        StubURLProtocol.reset { _ in (201, Data(#"{"status":"ok"}"#.utf8)) }
        let report = LocationReport(vehicleID: "bus-1", tripID: "T1", latitude: 47.6, longitude: -122.33,
                                    bearing: nil, speed: 8, accuracy: 5, timestamp: 1_756_800_000,
                                    routeID: "R1", startDate: "20260902")
        try await client().postLocation(report)
        let body = try JSONSerialization.jsonObject(with: StubURLProtocol.last!.body!) as! [String: Any]
        #expect(body["route_id"] as? String == "R1")
        #expect(body["start_date"] as? String == "20260902")
    }

    @Test func serverErrorsCarryStatusAndMessage() async throws {
        StubURLProtocol.reset { _ in (401, Data(#"{"error":"invalid token"}"#.utf8)) }
        await #expect(throws: APIError.status(401, message: "invalid token")) {
            try await client().myVehicles()
        }
    }

    @Test func redirectsAreNotFollowed() async throws {
        StubURLProtocol.reset { _ in (302, Data()) }
        await #expect(throws: APIError.status(302, message: "")) {
            try await client().myVehicles()
        }
        // Only the original request was made: the redirected request to
        // elsewhere.example.org was never sent, proving RedirectRefuser (and
        // its wiring into TrackerClient's session) actually refused it rather
        // than this test merely showing a bare 3xx surfaces as a status.
        #expect(StubURLProtocol.count == 1)
        #expect(StubURLProtocol.last?.url.host == "positions.example.org")
    }

    @Test func redirectRefuserAnswersNil() async {
        let refuser = RedirectRefuser()
        let task = URLSession.shared.dataTask(with: base)
        let response = HTTPURLResponse(url: base, statusCode: 302, httpVersion: "HTTP/1.1", headerFields: nil)!
        let newRequest = URLRequest(url: URL(string: "https://elsewhere.example.org")!)
        let result = await withCheckedContinuation { (continuation: CheckedContinuation<URLRequest?, Never>) in
            refuser.urlSession(URLSession.shared, task: task, willPerformHTTPRedirection: response, newRequest: newRequest) { req in
                continuation.resume(returning: req)
            }
        }
        #expect(result == nil)
    }

    @Test func undecodableBodyIsADecodingError() async throws {
        StubURLProtocol.reset { _ in (200, Data("nope".utf8)) }
        do {
            _ = try await client().myVehicles()
            Issue.record("expected a decoding error")
        } catch APIError.decoding {
        } catch {
            Issue.record("unexpected \(error)")
        }
    }

    // MARK: Refresh

    let now = Date(timeIntervalSince1970: 1_792_476_000)
    let busOne = [Vehicle(id: "bus-1", label: "Bus 1")]

    /// Signed in with a1 and r1, with no refresh due before the request.
    var signedIn: StoredToken {
        StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-60), refreshToken: "r1", expiresAt: now.addingTimeInterval(840))
    }

    func refreshingClient(_ tokens: any TokenStoring, refresher: TokenRefresher? = nil) throws -> TrackerClient {
        let now = now
        return try TrackerClient(baseURL: base, tokens: tokens, refresher: refresher ?? TokenRefresher(tokens: tokens, now: { now }),
                                 configuration: StubURLProtocol.configuration())
    }

    /// `/vehicles` takes only a2; `/auth/refresh` answers `refresh`.
    func serve(refresh: (Int, String)) {
        StubURLProtocol.reset { req in
            if req.url!.path == "/api/v1/auth/refresh" { return (refresh.0, Data(refresh.1.utf8)) }
            if req.value(forHTTPHeaderField: "Authorization") == "Bearer a2" {
                return (200, Data(#"[{"id":"bus-1","label":"Bus 1"}]"#.utf8))
            }
            return (401, Data(#"{"error":"invalid token"}"#.utf8))
        }
    }

    let renewedReply = (200, #"{"token":"a2","access_token":"a2","refresh_token":"r2","expires_in":900}"#)

    var requests: [StubURLProtocol.Recorded] { StubURLProtocol.recorded.withLock { $0 } }

    @Test func a401IsRefreshedAndTheRequestSentAgain() async throws {
        serve(refresh: renewedReply)
        let store = InMemoryTokenStore(signedIn)
        #expect(try await refreshingClient(store).myVehicles() == busOne)

        let sent = requests
        try #require(sent.map(\.url.path) == ["/api/v1/vehicles", "/api/v1/auth/refresh", "/api/v1/vehicles"])
        #expect(sent[0].headers["Authorization"] == "Bearer a1")
        #expect(sent[1].method == "POST")
        #expect(sent[1].headers["Authorization"] == nil)
        #expect(sent[1].headers["Content-Type"] == "application/json")
        #expect(try JSONSerialization.jsonObject(with: #require(sent[1].body)) as? [String: String] == ["refresh_token": "r1"])
        #expect(sent[2].headers["Authorization"] == "Bearer a2")
        #expect(try store.load() == StoredToken(token: "a2", issuedAt: now, refreshToken: "r2", expiresAt: now.addingTimeInterval(900)))
    }

    @Test func a401OnTheRetryIsNotRefreshedAgain() async throws {
        StubURLProtocol.reset { req in
            if req.url!.path == "/api/v1/auth/refresh" {
                return (200, Data(#"{"access_token":"a2","refresh_token":"r2","expires_in":900}"#.utf8))
            }
            return (401, Data(#"{"error":"invalid token"}"#.utf8))
        }
        await #expect(throws: APIError.status(401, message: "invalid token")) {
            try await refreshingClient(InMemoryTokenStore(signedIn)).myVehicles()
        }
        #expect(requests.map(\.url.path) == ["/api/v1/vehicles", "/api/v1/auth/refresh", "/api/v1/vehicles"])
    }

    @Test func aRefusedRefreshLetsTheOriginal401Through() async throws {
        serve(refresh: (401, #"{"error":"invalid or expired refresh token"}"#))
        let store = InMemoryTokenStore(signedIn)
        await #expect(throws: APIError.status(401, message: "invalid token")) {
            try await refreshingClient(store).myVehicles()
        }
        var dropped = signedIn
        dropped.refreshToken = nil
        #expect(try store.load() == dropped)
    }

    /// The reporter shows a transport error as No connection, which is
    /// true enough: the session is still good, and the next report retries.
    @Test(arguments: [500, 429])
    func aRefreshTheServerCannotDoIsATransportError(status: Int) async throws {
        serve(refresh: (status, #"{"error":"try later"}"#))
        let store = InMemoryTokenStore(signedIn)
        await #expect(throws: APIError.transport("refresh failed with HTTP \(status)")) {
            try await refreshingClient(store).myVehicles()
        }
        #expect(try store.load() == signedIn)
    }

    @Test func aRefreshReplyThatIsNotATokenPairKeepsTheRefreshToken() async throws {
        serve(refresh: (200, "<html>Sign in to the station Wi-Fi</html>"))
        let store = InMemoryTokenStore(signedIn)
        do {
            _ = try await refreshingClient(store).myVehicles()
            Issue.record("expected a transport error")
        } catch APIError.transport {
        } catch {
            Issue.record("unexpected \(error)")
        }
        #expect(try store.load() == signedIn)
    }

    @Test func aWrongPasswordAtSignInNeverRefreshes() async throws {
        StubURLProtocol.reset { _ in (401, Data(#"{"error":"invalid credentials"}"#.utf8)) }
        await #expect(throws: APIError.status(401, message: "invalid credentials")) {
            try await refreshingClient(InMemoryTokenStore(signedIn)).login(email: "d@test.com", password: "wrong")
        }
        #expect(requests.map(\.url.path) == ["/api/v1/auth/login"])
    }

    /// After a re-sign-in the trip's reporter and the pickers hold different
    /// clients. Both must wait for the refresh that is already out, not start
    /// their own: the stub would answer one, and it must not be asked.
    @Test func clientsSharingARefresherWaitForTheRefreshThatIsOut() async throws {
        serve(refresh: (200, #"{"access_token":"a9","refresh_token":"r9","expires_in":900}"#))
        let store = ScriptedTokenStore(signedIn)
        let now = now
        let refresher = TokenRefresher(tokens: store, now: { now })
        let held = ScriptedRefresh()
        async let first = refresher.afterUnauthorized(failedToken: "a1", refresh: held.call)
        #expect(await eventually { held.presented.count == 1 })

        let reporterClient = try refreshingClient(store, refresher: refresher)
        let pickerClient = try refreshingClient(store, refresher: refresher)
        async let fromReporter = reporterClient.myVehicles()
        async let fromPicker = pickerClient.myVehicles()
        // One read for the refresh that is out, and two for each client: to
        // send, then to ask the refresher after the 401.
        #expect(await eventually { store.loads == 5 })
        held.answer(.issued(AuthTokens(accessToken: "a2", refreshToken: "r2", expiresIn: 900)))

        #expect(try await fromReporter == busOne)
        #expect(try await fromPicker == busOne)
        #expect(await first == .retry("a2"))
        #expect(!requests.contains { $0.url.path == "/api/v1/auth/refresh" })
        #expect(held.presented == ["r1"])
    }

    /// Thirty seconds from expiry, so the refresh happens before the request.
    var aboutToExpire: StoredToken {
        StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-870), refreshToken: "r1", expiresAt: now.addingTimeInterval(30))
    }

    @Test func aTokenAboutToExpireIsRefreshedBeforeTheRequest() async throws {
        serve(refresh: renewedReply)
        let store = InMemoryTokenStore(aboutToExpire)
        #expect(try await refreshingClient(store).myVehicles() == busOne)
        #expect(requests.map(\.url.path) == ["/api/v1/auth/refresh", "/api/v1/vehicles"])
        #expect(requests.last?.headers["Authorization"] == "Bearer a2")
        #expect(try store.load()?.refreshToken == "r2")
    }

    @Test func aTokenWithTimeLeftGoesOutWithoutARefresh() async throws {
        StubURLProtocol.reset { _ in (200, Data(#"[{"id":"bus-1","label":"Bus 1"}]"#.utf8)) }
        #expect(try await refreshingClient(InMemoryTokenStore(signedIn)).myVehicles() == busOne)
        #expect(requests.map(\.url.path) == ["/api/v1/vehicles"])
        #expect(requests.last?.headers["Authorization"] == "Bearer a1")
    }

    /// The access token still works, so a refused or failed early refresh
    /// costs the request nothing, and the refresh token waits for the 401.
    @Test(arguments: [401, 500])
    func anEarlyRefreshThatFailsStillSendsTheRequest(status: Int) async throws {
        StubURLProtocol.reset { req in
            if req.url!.path == "/api/v1/auth/refresh" { return (status, Data(#"{"error":"no"}"#.utf8)) }
            return (200, Data(#"[{"id":"bus-1","label":"Bus 1"}]"#.utf8))
        }
        let store = InMemoryTokenStore(aboutToExpire)
        #expect(try await refreshingClient(store).myVehicles() == busOne)
        #expect(requests.map(\.url.path) == ["/api/v1/auth/refresh", "/api/v1/vehicles"])
        #expect(requests.last?.headers["Authorization"] == "Bearer a1")
        #expect(try store.load() == aboutToExpire)
    }

    /// A sign-out at the end of a shift finds the access token long expired.
    /// The logout is renewed first like any other request, or the server
    /// would refuse it and the session would live on there.
    @Test func aLogoutWithAnExpiredTokenIsRefreshedFirst() async throws {
        serve(refresh: renewedReply)
        let store = InMemoryTokenStore(StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-3600), refreshToken: "r1",
                                                   expiresAt: now.addingTimeInterval(-2700)))
        try await refreshingClient(store).logout()
        #expect(requests.map(\.url.path) == ["/api/v1/auth/refresh", "/api/v1/auth/logout"])
        #expect(requests.last?.headers["Authorization"] == "Bearer a2")
    }
}
