import Foundation
import Testing
@testable import VehicleTracker

/// The login and picker screens show `error.localizedDescription` as it comes,
/// so each case has to read as a sentence to a driver — not as the enum's own
/// name, which is what an `Error` without `LocalizedError` falls back to.
@Suite struct APIErrorTests {
    @Test func insecureURLExplainsThePolicy() {
        #expect(APIError.insecureURL.localizedDescription == ServerURLPolicy.explanation)
    }

    @Test func aStatusPrefersTheServersMessage() {
        #expect(APIError.status(409, message: "driver already has an active trip").localizedDescription
                == "driver already has an active trip")
    }

    @Test func aStatusWithoutAMessageNamesTheCode() {
        #expect(APIError.status(502, message: "").localizedDescription == "Server error 502")
    }

    @Test func transportNamesTheReach() {
        #expect(APIError.transport("offline").localizedDescription == "Could not reach the server: offline")
    }

    @Test func decodingHidesTheSwiftDetail() {
        #expect(APIError.decoding("keyNotFound(CodingKeys(stringValue: \"id\"))").localizedDescription
                == "The server's reply could not be read.")
    }

    @Test func tripSessionErrorsReadAsInstructions() {
        #expect(TripSessionError.notSignedIn.localizedDescription == "Sign in first.")
        #expect(TripSessionError.unusableTrip.localizedDescription == "This run has no shape or stops to follow.")
    }
}
