import Foundation

/// A refused start as the banner over the board shows it: what happened,
/// the one action that fixes it, or none when there is nothing to do here,
/// and the symbol beside it. A port of Android's `toTripError` and
/// `startBannerOf`. The server's own words are never shown: they are
/// written for the API, and `TripSession` logs them instead.
nonisolated struct StartRefusal: Equatable {
    enum Action: Equatable {
        case changeVehicle, tryAgain, reload, openSettings
    }

    let symbol: String
    let text: String
    let action: Action?

    init(_ error: any Error, vehicleID: String) {
        switch error {
        case APIError.status(403, _):
            self.init("hand.raised.fill", PickerText.notAssigned(vehicleID), .changeVehicle)
        case APIError.status(409, message: "vehicle already has an active trip"):
            // Another driver's trip: nothing to end on this phone.
            self.init("person.2.fill", PickerText.vehicleBusy(vehicleID), .changeVehicle)
        case APIError.status(409, message: "vehicle is inactive"):
            self.init("nosign", PickerText.vehicleInactive(vehicleID), .changeVehicle)
        case APIError.status(409, _), TripSessionError.busy:
            // An active trip ends on the phone that started it.
            self.init("info.circle.fill", PickerText.tripAlreadyRunning, nil)
        case APIError.status(422, _):
            self.init("calendar.badge.clock", PickerText.scheduleMovedOn, .reload)
        case APIError.transport:
            self.init("icloud.slash.fill", PickerText.cannotReachServer, .tryAgain)
        case TripSessionError.previousTripNotEnded:
            self.init("exclamationmark.arrow.triangle.2.circlepath", error.localizedDescription, .tryAgain)
        case TripSessionError.unusableTrip:
            // A run with nothing to follow is not fixed by trying it again.
            self.init("mappin.slash", error.localizedDescription, nil)
        case TripSessionError.locationNotAsked:
            // Nothing is off in Settings. Trying again asks again, now that
            // the driver is on the phone to answer.
            self.init("location.fill", error.localizedDescription, .tryAgain)
        case let refusal as TripSessionError where refusal.opensSettings:
            self.init(refusal == .preciseLocationOff ? "scope" : "location.slash.fill", refusal.localizedDescription, .openSettings)
        default:
            self.init("exclamationmark.circle.fill", PickerText.couldNotStart, .tryAgain)
        }
    }

    private init(_ symbol: String, _ text: String, _ action: Action?) {
        self.symbol = symbol
        self.text = text
        self.action = action
    }
}
