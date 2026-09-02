import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct RiderClientTests {
    let base = URL(string: "https://vp.example.org")!

    @Test func registerPostsAndDecodes() async throws {
        let t = FakeRideTransport()
        t.script("POST register", FakeRideTransport.ok(201, json: #"{"rider_id":"r1","token":"tok","report_interval_seconds":5,"max_batch_size":12}"#))
        let c = RiderClient(serverURL: base, transport: t)
        let r = try await c.register(installationID: "inst", appID: "org.test", appVersion: "1")
        #expect(r.riderID == "r1")
        let req = try #require(t.requests(matching: "POST register").first)
        #expect(req.path == "api/v1/rider/register")
        #expect(req.bearerToken == nil)
        let body = try #require(req.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
        #expect(obj["platform"] as? String == "ios")
        #expect(obj["installation_id"] as? String == "inst")
    }

    @Test func startRideSendsBearerAndPath() async throws {
        let t = FakeRideTransport()
        t.script("POST rides", FakeRideTransport.ok(201, json: #"{"ride_id":"ride1","state":"pending","report_interval_seconds":5,"max_batch_size":12}"#))
        let c = RiderClient(serverURL: base, transport: t)
        let r = try await c.startRide(token: "tok", trip: TripDescriptor(tripID: "T1"))
        #expect(r.rideID == "ride1")
        let req = try #require(t.requests(matching: "POST rides").first)
        #expect(req.bearerToken == "tok")
        #expect(req.path == "api/v1/rider/rides")
    }

    @Test func uploadAndEndPaths() async throws {
        let t = FakeRideTransport()
        t.script("POST positions", FakeRideTransport.ok(json: #"{"state":"pending","published":false,"corroboration":"unavailable","accepted":1,"ignored":0,"off_route_streak":0,"ended":false,"end_reason":""}"#))
        t.script("POST end", FakeRideTransport.ok(json: #"{"status":"ride ended","summary":{"points":1,"matched":1,"corroborated":0,"duration_seconds":10}}"#))
        let c = RiderClient(serverURL: base, transport: t)
        _ = try await c.uploadPositions(token: "tok", rideID: "ride1", positions: [PositionUpload(latitude: 1, longitude: 2, accuracy: nil, speed: nil, bearing: nil, timestamp: 3)])
        let end = try await c.endRide(token: "tok", rideID: "ride1", reason: .arrived)
        #expect(end.summary.points == 1)
        #expect(t.requests(matching: "POST positions").first?.path == "api/v1/rider/rides/ride1/positions")
        #expect(t.requests(matching: "POST end").first?.path == "api/v1/rider/rides/ride1/end")
    }

    @Test func tripStatusQuery() async throws {
        let t = FakeRideTransport()
        t.script("GET status", FakeRideTransport.ok(json: #"{"trip_id":"T1","start_date":"20260902","trusted":false,"rider_reported":true,"riders":2}"#))
        let c = RiderClient(serverURL: base, transport: t)
        let s = try await c.tripStatus(token: "tok", tripID: "T1", startDate: "20260902")
        #expect(s.riders == 2)
        let req = try #require(t.requests(matching: "GET status").first)
        #expect(req.path == "api/v1/rider/trips/T1/status")
        #expect(req.query == ["start_date": "20260902"])
    }

    @Test func errorMapping() async throws {
        let t = FakeRideTransport()
        t.script("POST positions",
                 FakeRideTransport.error(401, message: "invalid token"),
                 FakeRideTransport.error(409, message: "ride ended"),
                 FakeRideTransport.error(429, message: "rate limit exceeded"),
                 FakeRideTransport.transportFailure,
                 FakeRideTransport.ok(json: "not json"))
        let c = RiderClient(serverURL: base, transport: t)
        let pos = [PositionUpload(latitude: 1, longitude: 2, accuracy: nil, speed: nil, bearing: nil, timestamp: 3)]
        await #expect(throws: RideError.notAuthorized) { try await c.uploadPositions(token: "t", rideID: "r", positions: pos) }
        await #expect(throws: RideError.alreadyEnded) { try await c.uploadPositions(token: "t", rideID: "r", positions: pos) }
        await #expect(throws: RideError.server(status: 429, message: "rate limit exceeded")) { try await c.uploadPositions(token: "t", rideID: "r", positions: pos) }
        do { _ = try await c.uploadPositions(token: "t", rideID: "r", positions: pos); Issue.record("expected transport error") }
        catch let RideError.transport(msg) { #expect(!msg.isEmpty) }
        do { _ = try await c.uploadPositions(token: "t", rideID: "r", positions: pos); Issue.record("expected decoding error") }
        catch let RideError.decoding(msg) { #expect(!msg.isEmpty) }
    }
}
