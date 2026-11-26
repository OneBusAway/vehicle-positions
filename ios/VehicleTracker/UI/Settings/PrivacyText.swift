import Foundation

/// Every word Privacy and Licenses show, in Android's words (`privacy_*`,
/// `licenses_*`).
nonisolated enum PrivacyText {
    static var title: String {
        String(localized: "Privacy", comment: "Settings, About: the row, and the title of its page, on what the app sends and keeps.")
    }

    static var licensesTitle: String {
        String(localized: "Licenses", comment: "Settings, About: the row, and the title of its page, with the app's code and each license it is under.")
    }

    // MARK: The groups

    static var sentSection: String {
        String(localized: "What is sent", comment: "Privacy: the heading over what the app sends.")
    }

    static var whoSection: String {
        String(localized: "Who gets it", comment: "Privacy: the heading over who gets what the app sends.")
    }

    static var keptSection: String {
        String(localized: "What stays on this phone", comment: "Privacy: the heading over what the app keeps on the iPhone.")
    }

    static var moreSection: String {
        String(localized: "More", comment: "Privacy: the heading over the agency's privacy policy link.")
    }

    // MARK: What is sent

    static var trip: String {
        String(localized: "Only during a trip", comment: "Privacy, What is sent: the app takes positions only while a trip runs.")
    }

    static var tripDetail: String {
        String(localized: "Positions from Start trip until End trip. Nothing from before or after.", comment: "Privacy, under 'Only during a trip'.")
    }

    static var position: String {
        String(localized: "Where the vehicle is", comment: "Privacy, What is sent.")
    }

    static var positionDetail: String {
        String(localized: "Position, how accurate it is, speed, direction and time, with the vehicle and the run.", comment: "Privacy, under 'Where the vehicle is'.")
    }

    static var occupancy: String {
        String(localized: "How full it is", comment: "Privacy, What is sent: how full the vehicle is.")
    }

    static var occupancyDetail: String {
        String(localized: "Only if you say so on Tracking.", comment: "Privacy, under 'How full it is'.")
    }

    // MARK: Who gets it

    static var agency: String {
        String(localized: "Your agency", comment: "Privacy, Who gets it.")
    }

    /// "The server you signed in to, metro.example."
    static func agencyDetail(server: String?) -> String {
        guard let server else {
            return String(localized: "The server you signed in to.", comment: "Privacy, under 'Your agency', when there is no server to name.")
        }
        return String(localized: "The server you signed in to, \(server).", comment: "Privacy, under 'Your agency': the server's address, e.g. 'The server you signed in to, metro.example.'")
    }

    static var riders: String {
        String(localized: "Riders", comment: "Privacy, Who gets it.")
    }

    static var ridersDetail: String {
        String(localized: "They see the vehicle, not you.", comment: "Privacy, under 'Riders'.")
    }

    static var appleMaps: String {
        String(localized: "Apple Maps", comment: "Privacy, Who gets it: Apple's map service, by its own name.")
    }

    static var appleMapsDetail: String {
        String(localized: "Apple draws the map on Tracking. Reports never go to Apple.", comment: "Privacy, under 'Apple Maps'.")
    }

    // MARK: What stays on this phone

    static var account: String {
        String(localized: "Your email and the server", comment: "Privacy, What stays on this phone.")
    }

    static var accountDetail: String {
        String(localized: "The email goes when you sign out. The server stays.", comment: "Privacy, under 'Your email and the server'.")
    }

    static var kept: String {
        String(localized: "Reports with no connection", comment: "Privacy, What stays on this phone: the reports the app keeps until it can send them.")
    }

    static var keptDetail: String {
        String(localized: "Sent when there is a connection again. Any still here after a day are dropped.", comment: "Privacy, under 'Reports with no connection'.")
    }

    // MARK: More

    static var policy: String {
        String(localized: "Your agency's privacy policy", comment: "Privacy, More: a link to the agency's own privacy policy, which opens in Safari.")
    }

    static var policyDetail: String {
        String(localized: "Opens in the browser", comment: "Privacy, under 'Your agency's privacy policy'.")
    }

    // MARK: Licenses

    static var noOtherLibraries: String {
        String(localized: "The app uses no other libraries or fonts.", comment: "Licenses: under the list of the app's code and the code it took from OneBusAway for iOS.")
    }
}
