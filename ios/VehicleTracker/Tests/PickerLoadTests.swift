import Foundation
import Testing
@testable import VehicleTracker

@Suite struct PickerLoadTests {
    /// A 503 while the server's index loads, or a 404 from a server whose
    /// catalog was never registered: both mean there is no schedule to pick from.
    @Test func noScheduleIsA503OrA404() {
        #expect(PickerLoadFailure(APIError.status(503, message: "schedule data unavailable")) == .noSchedule)
        #expect(PickerLoadFailure(APIError.status(404, message: "")) == .noSchedule)
    }

    @Test(arguments: [
        APIError.status(500, message: "internal server error"),
        APIError.transport("The Internet connection appears to be offline."),
        APIError.decoding("missing key"),
    ])
    func anythingElseIsUnreachable(error: APIError) {
        #expect(PickerLoadFailure(error) == .unreachable)
    }

    @Test func aSessionErrorIsUnreachable() {
        #expect(PickerLoadFailure(TripSessionError.notSignedIn) == .unreachable)
    }
}
