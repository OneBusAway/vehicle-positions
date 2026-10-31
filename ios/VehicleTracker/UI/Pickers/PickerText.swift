import Foundation

/// Every word the picker screens and CarPlay's picker templates show, in
/// Android's words (`res/values/strings.xml`, `vehicles_*`, `routes_*`,
/// `runs_*`) with "vehicle" for "bus". One place for all of them, so the
/// phone and the car cannot differ.
enum PickerText {
    // MARK: Choose your vehicle

    static var vehiclesTitle: String {
        String(localized: "Choose your vehicle", comment: "Title of the first picker screen, the driver's assigned vehicles.")
    }

    static var vehicleSearchPrompt: String {
        String(localized: "Vehicle number", comment: "Placeholder of the vehicle screen's search field: the fleet number painted on the vehicle.")
    }

    static var favorites: String {
        String(localized: "Favorites", comment: "Section header over the vehicles the driver starred.")
    }

    static var recent: String {
        String(localized: "Recent", comment: "Section header over the vehicles or routes the driver used last.")
    }

    static var allVehicles: String {
        String(localized: "All vehicles", comment: "Section header over the rest of the driver's assigned vehicles.")
    }

    static var matches: String {
        String(localized: "Matches", comment: "Section header over the vehicles or routes that match what the driver typed.")
    }

    static func section(_ section: VehicleSection) -> String {
        switch section {
        case .favorites: favorites
        case .recent: recent
        case .all: allVehicles
        case .matches: matches
        }
    }

    static func noVehicleMatches(_ query: String) -> String {
        String(localized: "No vehicle matches \(query)", comment: "Empty state title when the vehicle search finds nothing; the argument is what the driver typed.")
    }

    static var checkFleetNumber: String {
        String(localized: "Check the fleet number on the vehicle.", comment: "Empty state text under 'No vehicle matches …'.")
    }

    static var noVehicles: String {
        String(localized: "No vehicles are assigned to you", comment: "Empty state title when the driver has no assigned vehicles.")
    }

    static var askDispatcher: String {
        String(localized: "Ask your dispatcher to assign one, then check again.", comment: "Empty state text under 'No vehicles are assigned to you'.")
    }

    static var checkAgain: String {
        String(localized: "Check again", comment: "Button that loads a picker's list again when it came back empty.")
    }

    static var vehiclesLoadFailed: String {
        String(localized: "Couldn't load your vehicles", comment: "Empty state title when the vehicle list could not be loaded.")
    }

    static var checkConnection: String {
        String(localized: "Check your connection and try again.", comment: "Empty state text under a 'Couldn't load …' title.")
    }

    static var tryAgain: String {
        String(localized: "Try again", comment: "Button that retries a load or a start that failed.")
    }

    /// The tile as VoiceOver reads it: the fleet number, then the label when
    /// it says more ("4317, Bus 4317").
    static func vehicleTile(id: String, label: String?) -> String {
        guard let label else { return id }
        return String(localized: "\(id), \(label)", comment: "VoiceOver label of a vehicle tile: the fleet number, then the vehicle's label.")
    }

    static var favorite: String {
        String(localized: "Favorite", comment: "VoiceOver value of a starred vehicle tile.")
    }

    static func addToFavorites(_ id: String) -> String {
        String(localized: "Add \(id) to favorites", comment: "Label of the star on a vehicle tile that is not yet a favourite; the argument is the fleet number.")
    }

    static func removeFromFavorites(_ id: String) -> String {
        String(localized: "Remove \(id) from favorites", comment: "Label of the star on a vehicle tile that is a favourite; the argument is the fleet number.")
    }
}
