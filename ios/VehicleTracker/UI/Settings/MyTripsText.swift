import Foundation

/// Every word My trips shows (the design's sheet 10). Its loading and Try
/// again are the pickers' (`PickerText`).
nonisolated enum MyTripsText {
    static var title: String {
        String(localized: "My trips", comment: "Settings, Account: the row, and the title of its page, which lists the trips the driver has made.")
    }

    static var today: String {
        String(localized: "Today", comment: "My trips: the title over the trips that started today.")
    }

    static var yesterday: String {
        String(localized: "Yesterday", comment: "My trips: the title over the trips that started yesterday.")
    }

    static func times(_ start: String, _ end: String, vehicle: String) -> String {
        String(localized: "\(start) to \(end) · Vehicle \(vehicle)", comment: "My trips: under where a trip went, when it started and ended, then the fleet number of its vehicle, e.g. '7:13 AM to 8:02 AM · Vehicle 4317'.")
    }

    static var noTrips: String {
        String(localized: "No trips yet", comment: "My trips: the title when the driver has ended no trip yet.")
    }

    static var noTripsDetail: String {
        String(localized: "A trip shows here after you end it.", comment: "My trips: under 'No trips yet'.")
    }

    static var loadFailed: String {
        String(localized: "Couldn't load your trips", comment: "My trips: the title when the list could not be loaded, and the banner when a refresh or the next page failed.")
    }
}
