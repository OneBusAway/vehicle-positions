import Foundation
import OSLog
import UserNotifications

/// `DriverAlerting` through local notifications. iOS does not show one while
/// the app is in the foreground, where the status card already says the same
/// thing, so it only reaches a driver whose phone is in a pocket or a mount.
@MainActor
final class NotificationDriverAlerts: DriverAlerting {
    private let center = UNUserNotificationCenter.current()
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "alerts")
    /// One identifier, so a second stop replaces the first rather than
    /// stacking alerts about the same trip.
    private let identifier = "reporting-stopped"

    func requestPermission() {
        center.requestAuthorization(options: [.alert, .sound]) { [log] _, error in
            if let error { log.warning("notification permission request failed: \(String(describing: error), privacy: .public)") }
        }
    }

    func reportingStopped(_ status: TripSession.ReportingStatus) {
        switch status {
        case .locationLost:
            post(body: String(localized: "Location stopped. Open the app and tap Resume.",
                              comment: "Notification body: the location stream ended mid-trip and only Resume in the app restarts it."))
        default:
            post(body: String(localized: "Open the app on iPhone to carry on reporting.",
                              comment: "Notification body: iOS stopped the app's location reports in the background until the driver opens it."))
        }
    }

    func tripNeedsResume() {
        post(body: String(localized: "The app closed during your trip. Open it to resume.",
                          comment: "Notification body: iOS relaunched the app in the background mid-trip, but the trip needs the driver to resume it."))
    }

    func reportingRecovered() {
        center.removePendingNotificationRequests(withIdentifiers: [identifier])
        center.removeDeliveredNotifications(withIdentifiers: [identifier])
    }

    private func post(body: String) {
        let content = UNMutableNotificationContent()
        content.title = String(localized: "Not reporting your position",
                               comment: "Notification title: the trip's reports have stopped and riders can't see the bus.")
        content.body = body
        content.sound = .default
        let request = UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
        center.add(request) { [log] error in
            if let error { log.warning("could not post the reporting alert: \(String(describing: error), privacy: .public)") }
        }
    }
}
