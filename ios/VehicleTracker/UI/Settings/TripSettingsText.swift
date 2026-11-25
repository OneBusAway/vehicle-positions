import Foundation

/// Every word During a trip shows, in Android's words (`trip_*`).
nonisolated enum TripSettingsText {
    static var title: String {
        String(localized: "During a trip", comment: "The title of a page opened from Settings, with choices for the Tracking screen. It names its row in Settings too.")
    }

    static var screen: String {
        String(localized: "Screen", comment: "During a trip: the heading over two choices of when the phone's display stays on, each over what it means.")
    }

    static func name(of choice: ScreenOn) -> String {
        switch choice {
        case .charging: String(localized: "On while charging", comment: "During a trip, under 'Screen': the phone's display stays on while it charges.")
        case .always: String(localized: "Always on", comment: "During a trip, under 'Screen': the phone's display stays on, charging or not.")
        }
    }

    static func detail(of choice: ScreenOn) -> String {
        switch choice {
        case .charging: String(localized: "On battery it goes dark as usual", comment: "During a trip, under 'On while charging': what the phone's display does when the phone is not charging.")
        case .always: String(localized: "Uses more battery", comment: "During a trip, under 'Always on'.")
        }
    }

    static var map: String {
        String(localized: "Map", comment: "During a trip: the heading over two choices, with a picture each, of what is at the top of the map.")
    }

    static func name(of choice: MapUp) -> String {
        switch choice {
        case .heading: String(localized: "The way you drive", comment: "During a trip, under 'Map': the map turns so that the way the vehicle goes is at the top.")
        case .north: String(localized: "North up", comment: "During a trip, under 'Map': the map has north at the top.")
        }
    }

    static var vibrate: String {
        String(localized: "Vibrate when reporting has a problem", comment: "During a trip: a switch, over the problems it is for.")
    }

    static var vibrateDetail: String {
        String(localized: "No connection, no GPS, or signed out", comment: "During a trip, under 'Vibrate when reporting has a problem': the problems the phone vibrates for.")
    }
}
