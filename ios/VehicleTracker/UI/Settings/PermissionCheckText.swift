import Foundation

/// Every word Permission check shows of its own, in Android's words
/// (`permission_check_*`). The rows' names and reasons are Before you start's,
/// from `ChecklistText`, as are its buttons': what iOS is asked and what can
/// be put right are the same there.
nonisolated enum PermissionCheckText {
    static var title: String {
        String(localized: "Permission check", comment: "The title of a page opened from Settings, with each thing the app needs, as on Before you start, and what fixes one that is missing. It names its row in Settings too, and is the VoiceOver hint on the top of Settings, which opens it.")
    }

    static var intro: String {
        String(localized: "What the app needs to report your vehicle.", comment: "Under the title 'Permission check'.")
    }

    static var riders: String {
        String(localized: "Riders", comment: "Permission check: the last row, after all the things the app needs: whether riders can see the vehicle.")
    }

    static var ridersSee: String {
        String(localized: "They see your vehicle during a trip.", comment: "Permission check, under 'Riders', when everything is allowed.")
    }

    static var ridersDoNotSee: String {
        String(localized: "They can't see you yet.", comment: "Permission check, under 'Riders', when something that stops a trip is missing.")
    }

    /// What a missing row's button says, as Before you start's does: Continue
    /// raises iOS's own prompt. Not "Allow": App Review rejects a button that
    /// says it before the prompt does (guideline 5.1.1(iv)).
    static func name(of fix: Readiness.Fix) -> String {
        switch fix {
        case .ask: ChecklistText.continue
        case .openSettings: ChecklistText.openSettings
        case .readAgain: ChecklistText.tryAgain
        }
    }

    /// A row's button as VoiceOver reads it, with the row's name, as TalkBack
    /// does on Android.
    static func spoken(_ fix: Readiness.Fix, for row: LocationChecklist.Row) -> String {
        let title = ChecklistText.title(of: row), button = name(of: fix)
        return String(localized: "\(title), \(button)", comment: "VoiceOver: a button on Permission check: its row's title, then what the button says, e.g. 'Location, Open Settings'.")
    }

    /// One announcement for a change while the page shows: each row that
    /// changed, in order, so no part cuts off the one before it. A row allowed
    /// now says only that. Nil when no row changed.
    static func announcement(from old: Readiness, to new: Readiness) -> String? {
        let parts = LocationChecklist.Row.allCases.compactMap { row -> String? in
            let state = new.checklistState(of: row)
            guard state != old.checklistState(of: row) else { return nil }
            return state == .allowed ? ChecklistText.allowedNow(row) : ChecklistText.spoken(row, state)
        }
        return parts.isEmpty ? nil : parts.joined(separator: " ")
    }
}
