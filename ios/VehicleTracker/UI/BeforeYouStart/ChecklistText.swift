import Foundation

/// Every word Before you start shows, in Android's words
/// (`res/values/strings.xml`, `preflight_*`) with "vehicle" for "bus" and
/// "iOS" for "Android".
nonisolated enum ChecklistText {
    static var title: String {
        String(localized: "Before you start", comment: "Title of the checklist shown before a trip when the app is missing location access.")
    }

    static func message(vehicle: String) -> String {
        String(localized: "The app needs these to report vehicle \(vehicle). iOS asks about each one next.", comment: "Under the checklist's title: the vehicle's fleet number, e.g. 'The app needs these to report vehicle 4317. iOS asks about each one next.'")
    }

    static func title(of row: LocationChecklist.Row) -> String {
        switch row {
        case .location:
            String(localized: "Location", comment: "Checklist row: the app's location access.")
        case .precise:
            String(localized: "Precise location", comment: "Checklist row: precise rather than approximate location.")
        case .locationServices:
            String(localized: "Location services", comment: "Checklist row: Location Services for the whole phone.")
        }
    }

    /// The row's reason, or what replaced it once the row was answered.
    static func reason(of row: LocationChecklist.Row, _ state: LocationChecklist.RowState) -> String {
        switch (row, state) {
        case (.location, .neededInSettings):
            String(localized: "Location is needed to report", comment: "Checklist: in place of the Location row's reason once location was refused.")
        case (.precise, .approximate):
            String(localized: "Only approximate was allowed", comment: "Checklist: in place of the Precise location row's reason once only approximate location was allowed.")
        case (.location, _):
            String(localized: "So the app knows where the vehicle is", comment: "Checklist: why the app needs location.")
        case (.precise, _):
            String(localized: "So riders see the vehicle at the right stop", comment: "Checklist: why the app needs precise location.")
        case (.locationServices, _):
            String(localized: "Location must be on for the whole phone", comment: "Checklist: why Location Services must be on.")
        }
    }

    static var allowed: String {
        String(localized: "Allowed", comment: "At the end of a checklist row that iOS allowed.")
    }

    static var next: String {
        String(localized: "checklist.next", defaultValue: "Next", comment: "At the end of the checklist row that Continue asks iOS about next.")
    }

    static var tryAgain: String {
        String(localized: "Try again", comment: "Checklist row button: ask iOS for precise location again, or check Location Services again. Permission check and the button under 'Not ready to report' in the app's own Settings check Location Services again with it.")
    }

    static var openSettings: String {
        String(localized: "Open Settings", comment: "Checklist row button, Permission check's, and the button under 'Not ready to report' in the app's own Settings, that opens the app's page in the Settings app, where location can be allowed.")
    }

    static var `continue`: String {
        String(localized: "Continue", comment: "The checklist's button, which asks iOS about the next row, and the one on Permission check's Location row, which asks iOS about location.")
    }

    static var cancel: String {
        String(localized: "Cancel", comment: "Closes the checklist without starting the run, once nothing on it asks iOS by itself.")
    }

    /// What VoiceOver says when iOS allows a row while the sheet shows it,
    /// as TalkBack says it on Android.
    static func allowedNow(_ row: LocationChecklist.Row) -> String {
        let name = title(of: row)
        return String(localized: "\(name), allowed.", comment: "VoiceOver: said once when iOS allows a checklist row while the checklist shows: the row's title, e.g. 'Location, allowed.'")
    }

    /// One announcement for a change on the sheet: each row that changed,
    /// in order, so no part cuts off the one before it. A row allowed now
    /// says only that. Nil when no row changed.
    static func announcement(from old: LocationChecklist, to new: LocationChecklist) -> String? {
        let parts = LocationChecklist.Row.allCases.compactMap { row -> String? in
            let state = new.state(of: row)
            guard state != old.state(of: row) else { return nil }
            return state == .allowed ? allowedNow(row) : spoken(row, state)
        }
        return parts.isEmpty ? nil : parts.joined(separator: " ")
    }

    /// One row as VoiceOver reads it, e.g. "Location. So the app knows where
    /// the vehicle is. Next."
    static func spoken(_ row: LocationChecklist.Row, _ state: LocationChecklist.RowState) -> String {
        let name = title(of: row), why = reason(of: row, state)
        switch state {
        case .next: return String(localized: "\(name). \(why). Next.", comment: "VoiceOver: a checklist row that Continue asks about next: its title, then why the app needs it, e.g. 'Location. So the app knows where the vehicle is. Next.'")
        case .allowed: return String(localized: "\(name). \(why). Allowed.", comment: "VoiceOver: a checklist row iOS allowed: its title, then why the app needs it, e.g. 'Precise location. So riders see the vehicle at the right stop. Allowed.'")
        case .neededInSettings, .approximate, .off, .notReached:
            return String(localized: "\(name). \(why).", comment: "VoiceOver: a checklist row: its title, then why the app needs it or what went wrong, e.g. 'Precise location. Only approximate was allowed.'")
        }
    }
}
