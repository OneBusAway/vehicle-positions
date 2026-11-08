import Foundation
import Testing
import UIKit
@testable import VehicleTracker

/// A port of Android's `StartBannerTest`: each refusal with its sentence and
/// its one action, as the banner over the board shows them.
@Suite struct StartRefusalTests {
    func refusal(_ error: any Error) -> StartRefusal {
        StartRefusal(error, vehicleID: "4317")
    }

    /// One line per refusal: the symbol, the sentence, the action.
    func row(_ refusal: StartRefusal) -> String {
        "\(refusal.symbol) | \(refusal.text) | \(refusal.action.map { "\($0)" } ?? "none")"
    }

    @Test func eachRefusedStartSaysWhatHappenedAndOffersTheOneActionThatFixesIt() {
        #expect(row(refusal(APIError.status(403, message: "driver is not assigned to this vehicle")))
            == "hand.raised.fill | You're not assigned to vehicle 4317. | changeVehicle")
        #expect(row(refusal(APIError.status(409, message: "driver already has an active trip")))
            == "info.circle.fill | You already have a trip running. End it before starting another. | none")
        #expect(row(refusal(TripSessionError.busy))
            == "info.circle.fill | You already have a trip running. End it before starting another. | none")
        #expect(row(refusal(TripSessionError.previousTripNotEnded))
            == "exclamationmark.arrow.triangle.2.circlepath | Your previous trip could not be ended yet. Check your connection and try again. | tryAgain")
        #expect(row(refusal(APIError.transport("The Internet connection appears to be offline.")))
            == "icloud.slash.fill | Can't reach the server. Check your connection. | tryAgain")
        #expect(row(refusal(TripSessionError.unusableTrip))
            == "mappin.slash | This run has no route or stops to follow. Pick another run. | none")
        #expect(row(refusal(APIError.status(422, message: "trip not active on date")))
            == "calendar.badge.clock | The schedule moved on to a new day. | reload")
        #expect(row(refusal(TripSessionError.locationDenied))
            == "location.slash.fill | Location is off for OBA Vehicle Tracker. Turn it on in Settings to start a trip. | openSettings")
        #expect(row(refusal(TripSessionError.locationNotAsked))
            == "location.fill | Open OBA Vehicle Tracker on your iPhone to allow location. | tryAgain")
        #expect(row(refusal(TripSessionError.locationServicesOff))
            == "location.slash.fill | Location Services are off on this iPhone. Turn them on in Settings > Privacy & Security > Location Services. | openSettings")
        #expect(row(refusal(TripSessionError.preciseLocationOff))
            == "scope | Precise Location is off. Turn it on in Settings so the vehicle shows on the right street. | openSettings")
    }

    @Test(arguments: [
        APIError.status(500, message: "failed to start trip"),
        APIError.status(404, message: "not found"),
        APIError.decoding("missing key"),
        TripSessionError.notSignedIn,
    ] as [any Error])
    func anythingElseCouldNotStartTheTrip(error: any Error) {
        #expect(row(refusal(error)) == "exclamationmark.circle.fill | Couldn't start the trip. | tryAgain")
    }

    /// The server writes its messages for the API, not the driver.
    @Test func theServersOwnWordsAreNeverShown() {
        for error in [APIError.status(403, message: "driver is not assigned to this vehicle"),
                      APIError.status(409, message: "driver already has an active trip"),
                      APIError.status(500, message: "failed to start trip")] {
            #expect(!refusal(error).text.contains("driver"), "\(error)")
        }
    }

    /// A session error's sentence lives on the error, so the banner and the
    /// car read the same one.
    @Test(arguments: [TripSessionError.unusableTrip, .previousTripNotEnded, .locationDenied, .locationNotAsked,
                      .locationServicesOff, .preciseLocationOff])
    func aSessionErrorsRowReadsItsOwnSentence(error: TripSessionError) {
        #expect(refusal(error).text == error.localizedDescription)
    }

    @Test(arguments: [
        "hand.raised.fill", "info.circle.fill", "exclamationmark.arrow.triangle.2.circlepath", "icloud.slash.fill",
        "mappin.slash", "calendar.badge.clock", "location.slash.fill", "location.fill", "scope", "exclamationmark.circle.fill",
        // The empty states and the tiles.
        "bus", "icloud.slash", "magnifyingglass", "calendar", "calendar.badge.exclamationmark", "star", "star.fill", "star.slash",
    ])
    func everySymbolIsInTheSystemSet(symbol: String) {
        #expect(UIImage(systemName: symbol) != nil)
    }
}
