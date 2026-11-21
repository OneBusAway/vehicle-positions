import Foundation
import Testing
@testable import VehicleTracker

/// Each error reads as a sentence, not as the enum's own name, which is what
/// an `Error` without `LocalizedError` falls back to: the run board and
/// CarPlay show `TripSessionError`'s. An `APIError`'s is the app's own words,
/// never the server's English or the system's.
@Suite struct APIErrorTests {
    @Test func insecureURLSaysWhatSignInSays() {
        #expect(APIError.insecureURL.localizedDescription == SignInText.useHTTPS)
    }

    @Test(arguments: [
        APIError.status(409, message: "driver already has an active trip"),
        APIError.status(409, message: ""),
    ])
    func aStatusNamesTheCodeAndNeverTheServersMessage(error: APIError) {
        #expect(error.localizedDescription == "Server error 409")
    }

    @Test func transportSaysWhatAStartSays() {
        let error = APIError.transport("The Internet connection appears to be offline.")
        #expect(error.localizedDescription == PickerText.cannotReachServer)
        #expect(!error.localizedDescription.contains("Internet"))
    }

    @Test func decodingHidesTheSwiftDetail() {
        #expect(APIError.decoding("keyNotFound(CodingKeys(stringValue: \"id\"))").localizedDescription
                == "The server's reply could not be read.")
    }

    @Test func tripSessionErrorsReadAsInstructions() {
        #expect(TripSessionError.notSignedIn.localizedDescription == "Sign in first.")
        #expect(TripSessionError.unusableTrip.localizedDescription == "This run has no route or stops to follow. Pick another run.")
        #expect(TripSessionError.previousTripNotEnded.localizedDescription
            == "Your previous trip could not be ended yet. Check your connection and try again.")
    }
}
