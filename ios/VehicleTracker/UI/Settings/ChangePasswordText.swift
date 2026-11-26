import Foundation

/// Every word Change password shows (the design's sheet 09).
nonisolated enum ChangePasswordText {
    static var title: String {
        String(localized: "Change password", comment: "Settings, Account: the row, the title of its page and the button at its foot, which changes the driver's password.")
    }

    static var current: String {
        String(localized: "Current password", comment: "Change password: the field for the password the driver signs in with now.")
    }

    static var new: String {
        String(localized: "New password", comment: "Change password: the field for the password to sign in with from now on.")
    }

    static var again: String {
        String(localized: "New password again", comment: "Change password: the field where the new password is typed a second time, to catch a slip.")
    }

    static var rule: String {
        String(localized: "At least 8 characters", comment: "Change password: under the new password fields, what a password needs.")
    }

    static var othersSignedOut: String {
        String(localized: "Other phones signed in as you are signed out. This one stays signed in.", comment: "Change password: under the fields, what a change does to the driver's other sign-ins.")
    }

    static var wrongCurrent: String {
        String(localized: "That's not your current password.", comment: "Change password: under Current password, when the server turned it down.")
    }

    static var tooShort: String {
        String(localized: "Use at least 8 characters.", comment: "Change password: under New password, when it is shorter than 8 characters.")
    }

    static var tooLong: String {
        String(localized: "Use a shorter password.", comment: "Change password: under New password, when it is longer than the server takes (72 bytes).")
    }

    static var noMatch: String {
        String(localized: "The two new passwords don't match.", comment: "Change password: under New password again, when it is not the same as New password.")
    }

    static var changing: String {
        String(localized: "Changing password", comment: "Change password: the button while the change is with the server, beside a spinner.")
    }

    static func cannotReach(_ server: String) -> String {
        String(localized: "Can't reach \(server). Check your connection.", comment: "Change password: banner when nothing answered. The server's address as the driver knows it, e.g. 'tracker.example.org'.")
    }

    static var olderServer: String {
        String(localized: "Your agency's server can't change passwords yet.", comment: "Change password: banner when the server is a version from before drivers could change their password.")
    }

    static var failed: String {
        String(localized: "Couldn't change your password. Try again.", comment: "Change password: banner when the server answered with an error the driver can do nothing about but try again.")
    }

    static var changed: String {
        String(localized: "Password changed", comment: "Settings: the title of the alert after the driver's password was changed.")
    }

    static var changedDetail: String {
        String(localized: "Other phones were signed out.", comment: "Settings: the alert after the driver's password was changed: the other phones signed in as them were signed out.")
    }
}
