import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct WireTypesTests {
    private func json(_ data: Data) throws -> [String: Any] {
        try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    @Test func registerRequestUsesSnakeCaseAndNullAttestation() throws {
        let req = RegisterRequest(installationID: "7d1d", platform: "ios", appID: "org.onebusaway.iphone", appVersion: "26.4.0", attestation: nil)
        let obj = try json(RiderAPICodec.encode(req))
        #expect(obj["installation_id"] as? String == "7d1d")
        #expect(obj["app_id"] as? String == "org.onebusaway.iphone")
        #expect(obj["app_version"] as? String == "26.4.0")
        #expect(obj["platform"] as? String == "ios")
        #expect(obj.keys.contains("attestation"), "attestation must be sent as explicit null")
        #expect(obj["attestation"] is NSNull)
    }

    @Test func registerResponseDecodes() throws {
        let data = Data(#"{"rider_id":"a3f0","token":"jwt","report_interval_seconds":5,"max_batch_size":12}"#.utf8)
        let resp = try RiderAPICodec.decode(RegisterResponse.self, from: data)
        #expect(resp == RegisterResponse(riderID: "a3f0", token: "jwt", reportIntervalSeconds: 5, maxBatchSize: 12))
    }

    @Test func startRideRequestOmitsNilKeys() throws {
        let trip = TripDescriptor(tripID: "1_604321", startDate: "20260902", destinationStopID: "1_75414")
        let obj = try json(RiderAPICodec.encode(trip))
        #expect(obj["trip_id"] as? String == "1_604321")
        #expect(obj["start_date"] as? String == "20260902")
        #expect(obj["destination_stop_id"] as? String == "1_75414")
        #expect(obj["route_id"] == nil)
        #expect(obj["vehicle_id"] == nil)
        #expect(obj["boarding_stop_id"] == nil)
    }

    @Test func startRideResponseDecodesWithAndWithoutDestination() throws {
        let with = Data(#"{"ride_id":"c9e2","state":"pending","report_interval_seconds":5,"max_batch_size":12,"destination":{"stop_id":"1_75414","latitude":47.61,"longitude":-122.33}}"#.utf8)
        let r = try RiderAPICodec.decode(StartRideResponse.self, from: with)
        #expect(r.rideID == "c9e2")
        #expect(r.state == .pending)
        #expect(r.destination?.coordinate == Coordinate(latitude: 47.61, longitude: -122.33))
        let without = Data(#"{"ride_id":"c9e2","state":"pending","report_interval_seconds":5,"max_batch_size":12}"#.utf8)
        #expect(try RiderAPICodec.decode(StartRideResponse.self, from: without).destination == nil)
    }

    @Test func positionsRequestOmitsAbsentOptionals() throws {
        let req = PositionsRequest(positions: [
            PositionUpload(latitude: 47.60, longitude: -122.33, accuracy: 8, speed: nil, bearing: 184, timestamp: 1_756_800_000),
        ])
        let obj = try json(RiderAPICodec.encode(req))
        let positions = try #require(obj["positions"] as? [[String: Any]])
        #expect(positions.count == 1)
        #expect(positions[0]["latitude"] as? Double == 47.60)
        #expect(positions[0]["longitude"] as? Double == -122.33)
        #expect(positions[0]["accuracy"] as? Double == 8)
        #expect(positions[0]["speed"] == nil)
        #expect(positions[0]["bearing"] as? Double == 184)
        #expect(positions[0]["timestamp"] as? Int == 1_756_800_000)
    }

    @Test func positionsResponseDecodes() throws {
        let data = Data(#"{"state":"verified","published":true,"corroboration":"corroborated","accepted":3,"ignored":0,"off_route_streak":0,"ended":false,"end_reason":""}"#.utf8)
        let r = try RiderAPICodec.decode(PositionsResponse.self, from: data)
        #expect(r.state == .verified)
        #expect(r.published)
        #expect(r.corroboration == .corroborated)
        #expect(r.accepted == 3)
        #expect(r.endReasonValue == nil)
        let ended = Data(#"{"state":"rejected","published":false,"corroboration":"none","accepted":5,"ignored":1,"off_route_streak":5,"ended":true,"end_reason":"off_route"}"#.utf8)
        #expect(try RiderAPICodec.decode(PositionsResponse.self, from: ended).endReasonValue == .offRoute)
    }

    @Test func endRideRoundTrip() throws {
        let obj = try json(RiderAPICodec.encode(EndRideRequest(reason: .userRequested)))
        #expect(obj["reason"] as? String == "user_requested")
        let data = Data(#"{"status":"ride ended","summary":{"points":142,"matched":139,"corroborated":88,"duration_seconds":1260}}"#.utf8)
        let r = try RiderAPICodec.decode(EndRideResponse.self, from: data)
        #expect(r.summary == RideSummary(points: 142, matched: 139, corroborated: 88, durationSeconds: 1260))
    }

    @Test func tripStatusDecodes() throws {
        let data = Data(#"{"trip_id":"T1","start_date":"20260902","trusted":true,"rider_reported":false,"riders":0}"#.utf8)
        let s = try RiderAPICodec.decode(TripStatus.self, from: data)
        #expect(s == TripStatus(tripID: "T1", startDate: "20260902", trusted: true, riderReported: false, riders: 0))
    }

    @Test func endReasonWireValuesAndClientReportability() {
        // The whole table, in the order spec §4.4 lists it: raw value on the
        // wire, and whether a client may claim it.
        let expected: [(RideEndReason, String, Bool)] = [
            (.userRequested, "user_requested", true),
            (.arrived, "arrived", true),
            (.stationary, "stationary", true),
            (.maxDuration, "max_duration", true),
            (.locationUnavailable, "location_unavailable", true),
            (.authorizationDenied, "authorization_denied", true),
            (.networkFailure, "network_failure", true),
            (.appTerminated, "app_terminated", true),
            (.offRoute, "off_route", false),
            (.contradicted, "contradicted", false),
            (.implausible, "implausible", false),
            (.offSchedule, "off_schedule", false),
            (.superseded, "superseded", false),
            (.serverRestart, "server_restart", false),
            (.idle, "idle", false),
        ]
        #expect(RideEndReason.allCases.count == expected.count, "a new end reason needs a row here")
        for (reason, raw, reportable) in expected {
            #expect(reason.rawValue == raw)
            #expect(reason.isClientReportable == reportable, "\(raw)")
            #expect(RideEndReason(rawValue: raw) == reason)
        }
        #expect(RideEndReason.allCases.filter(\.isClientReportable).count == 8)
    }

    @Test func unknownEnumValuesDecodeRatherThanThrow() throws {
        // A server that grows a new state or corroboration value must not make
        // an older app throw in the middle of a ride.
        let data = Data(#"{"state":"weird","published":false,"corroboration":"sideways","accepted":1,"ignored":0,"off_route_streak":0,"ended":false,"end_reason":""}"#.utf8)
        let r = try RiderAPICodec.decode(PositionsResponse.self, from: data)
        #expect(r.state == .unknown)
        #expect(r.corroboration == .unknown)
        #expect(r.accepted == 1)

        let start = Data(#"{"ride_id":"c9e2","state":"quantum","report_interval_seconds":5,"max_batch_size":12}"#.utf8)
        #expect(try RiderAPICodec.decode(StartRideResponse.self, from: start).state == .unknown)
        #expect(try RiderAPICodec.decode(RideState.self, from: Data(#""verified""#.utf8)) == .verified)
        // And the round trip still spells the known cases the way the server does.
        #expect(String(data: try RiderAPICodec.encode(RideState.rejected), encoding: .utf8) == #""rejected""#)
        #expect(String(data: try RiderAPICodec.encode(Corroboration.unknown), encoding: .utf8) == #""unknown""#)
    }

    @Test func serverErrorBodyDecodes() throws {
        #expect(try RiderAPICodec.decode(ServerErrorBody.self, from: Data(#"{"error":"ride ended"}"#.utf8)).error == "ride ended")
    }
}
