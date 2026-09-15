import Foundation
import Testing
@testable import VehicleTracker

@Suite(.serialized) struct TrackerClientTests {
    let base = URL(string: "https://positions.example.org")!

    func client(token: String? = "tok") throws -> TrackerClient {
        try TrackerClient(baseURL: base, token: { token }, configuration: StubURLProtocol.configuration())
    }

    @Test func refusesAnInsecureBaseURL() {
        #expect(throws: APIError.insecureURL) {
            try TrackerClient(baseURL: URL(string: "http://positions.example.org")!, token: { nil })
        }
    }

    @Test func loginPostsCredentialsWithoutAToken() async throws {
        StubURLProtocol.reset { _ in (200, Data(#"{"token":"jwt-1"}"#.utf8)) }
        let token = try await client(token: nil).login(email: "d@test.com", password: "pw")
        #expect(token == "jwt-1")
        let r = try #require(StubURLProtocol.last)
        #expect(r.method == "POST")
        #expect(r.url.path == "/api/v1/auth/login")
        #expect(r.headers["Authorization"] == nil)
        #expect(r.headers["Content-Type"] == "application/json")
        let body = try JSONSerialization.jsonObject(with: r.body!) as! [String: String]
        #expect(body == ["email": "d@test.com", "password": "pw"])
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
}
