import OSLog
import UIKit

/// Tells a background relaunch by iOS from a launch the driver makes. iOS
/// relaunches an app that had a background location session when it was
/// terminated mid-trip, without showing it: no scene comes to the
/// foreground, on the phone or in the car.
enum BackgroundRelaunch {
    /// Whether nothing of the app is on screen. A scene still being connected
    /// (`.unattached`) is one coming to the foreground, so it counts as on
    /// screen.
    static func isInBackground(application: UIApplication.State, scenes: [UIScene.ActivationState]) -> Bool {
        application == .background && scenes.allSatisfy { $0 == .background }
    }

    /// Checks once the launch has connected its scenes (the next turn of the
    /// main queue) and carries on the stored trip if no one can see the app.
    static func resumeIfInBackground(_ session: TripSession) {
        DispatchQueue.main.async {
            let app = UIApplication.shared
            let scenes = app.connectedScenes.map(\.activationState)
            let background = isInBackground(application: app.applicationState, scenes: scenes)
            // What the decision saw, for checking it on a device in Console
            // (docs/ios-smoke-test.md, Check 4).
            Logger(subsystem: "org.onebusaway.vehicletracker", category: "launch").notice(
                "launch: application \(String(describing: app.applicationState.rawValue), privacy: .public), scenes \(String(describing: scenes.map(\.rawValue)), privacy: .public), background relaunch \(background, privacy: .public)")
            guard background else { return }
            session.resumeAfterBackgroundLaunch()
        }
    }
}
