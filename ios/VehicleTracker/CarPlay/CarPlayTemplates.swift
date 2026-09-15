import CarPlay
import UIKit

/// Builds the pieces of the car screen from session state. Pure functions of
/// their inputs, so each is tested without a car; the controller only wires
/// them to the interface controller.
@MainActor
enum CarPlayTemplates {
    /// A label in the navigation bar: a bar button that does nothing and
    /// looks disabled, which is the only text a map template can show there.
    static func titleButton(_ title: String) -> CPBarButton {
        let button = CPBarButton(title: title) { _ in }
        button.isEnabled = false
        return button
    }

    /// Navigation bar for the phases with no trip on the map (spec §6.2).
    static func idleBarButtons(phase: TripSession.Phase, onStart: @escaping () -> Void) -> (leading: [CPBarButton], trailing: [CPBarButton]) {
        switch phase {
        case .signedOut:
            return ([titleButton(String(localized: "Sign in on iPhone"))], [])
        case .starting:
            return ([titleButton(String(localized: "Starting…"))], [])
        default:
            let start = CPBarButton(title: String(localized: "Start trip")) { _ in onStart() }
            return ([titleButton(String(localized: "OBA Vehicle Tracker"))], [start])
        }
    }
}
