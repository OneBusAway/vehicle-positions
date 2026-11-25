import Foundation

/// Every word Help shows or says of its own (walkthrough spec §9, the iOS
/// column). The rows' titles are not here: each is the word Tracking shows,
/// from `TripStatusText`, so Help cannot drift from the screen it explains.
nonisolated enum HelpText {
    static var title: String {
        String(localized: "Help", comment: "Settings' row that opens Help, and the title of that page.")
    }

    static var welcomeAgain: String {
        String(localized: "See the welcome again", comment: "Help: the row that opens the three welcome screens again.")
    }

    static var welcomeAgainDetail: String {
        String(localized: "3 short screens", comment: "Help: under 'See the welcome again'.")
    }

    // MARK: On the status card

    static var statusSection: String {
        String(localized: "On the status card", comment: "Help: the heading over what the status card on Tracking can say.")
    }

    static var onTimeMeaning: String {
        String(localized: "You are on the timetable.", comment: "Help, under 'On time'.")
    }

    static var lateMeaning: String {
        String(localized: "Behind the timetable.", comment: "Help, under a status like '6 min late'.")
    }

    static var earlyMeaning: String {
        String(localized: "Ahead of the timetable. Riders may miss you.", comment: "Help, under a status like '3 min early'.")
    }

    static var offScheduleMeaning: String {
        String(localized: "Far from the timetable. Check you chose the right run.", comment: "Help, under 'Off schedule'.")
    }

    static var offRouteMeaning: String {
        String(localized: "Away from the route on the map.", comment: "Help, under 'Off route'.")
    }

    static var waitingForGPSMeaning: String {
        String(localized: "The iPhone is finding its position.", comment: "Help, under 'Waiting for GPS'.")
    }

    static var reportsWaitingMeaning: String {
        String(localized: "Saved with no connection. They are sent once it is back.", comment: "Help, under a line like '3 reports to send when online': the reports were saved on the iPhone while it had no connection.")
    }

    // MARK: Under the card

    static var problemSection: String {
        String(localized: "Under the card", comment: "Help: the heading over the lines that can show under the status card on Tracking, and the card that takes its place when signed out.")
    }

    static var noConnectionMeaning: String {
        String(localized: "Keep driving. Reports are saved and sent later.", comment: "Help, under 'No connection': the iPhone keeps the reports it can't send and sends them once the connection is back.")
    }

    static var noGPSMeaning: String {
        String(localized: "Riders see your last position until it comes back.", comment: "Help, under 'No GPS signal'.")
    }

    static var locationOffMeaning: String {
        String(localized: "Tap the line. It opens Settings to turn it on.", comment: "Help, under 'Location off or Precise Location off': tapping that line on Tracking opens the app's page in Settings.")
    }

    static var locationStoppedMeaning: String {
        String(localized: "Tap Resume on the same line.", comment: "Help, under 'Location stopped'. Resume is the button on that line.")
    }

    static var clockWrongMeaning: String {
        String(localized: "Turn on automatic time in Settings.", comment: "Help, under 'Phone clock is wrong'.")
    }

    static var refusedMeaning: String {
        String(localized: "The server is not taking this vehicle's reports. Ask your dispatcher.", comment: "Help, under 'Reports refused'.")
    }

    static var serverErrorMeaning: String {
        String(localized: "A problem on the server. Tell your dispatcher.", comment: "Help, under 'Server error'.")
    }

    static var signedOutMeaning: String {
        String(localized: "Reports have stopped. Sign in to go on. Your trip is still open.", comment: "Help, under 'Signed out'.")
    }

    static var openOnIPhoneMeaning: String {
        String(localized: "With CarPlay. Reports start when you open the app.", comment: "Help, under 'Open the app on iPhone': the line shows when a trip started from the car cannot report until the app is opened on the phone.")
    }
}
