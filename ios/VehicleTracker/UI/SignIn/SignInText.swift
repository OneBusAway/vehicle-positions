import Foundation

/// Every word Sign in shows, in Android's words (`res/values/strings.xml`,
/// `login_*`), plus the iOS-only rows: too many tries, the http:// rule and
/// a server the agency sets.
nonisolated enum SignInText {
    static var title: String {
        String(localized: "Sign in", comment: "Title of the sign-in screen.")
    }

    static var subtitle: String {
        String(localized: "Use your driver account for this agency.", comment: "Under the sign-in screen's title.")
    }

    static var serverAddress: String {
        String(localized: "Server address", comment: "Header over the field for the agency's server address, and that field's VoiceOver label.")
    }

    /// The server field's placeholder: an example address, the same in every language.
    static let serverPlaceholder = "https://tracker.example.org"

    static var email: String {
        String(localized: "Email", comment: "Label of the sign-in screen's email field.")
    }

    static var password: String {
        String(localized: "Password", comment: "Label of the sign-in screen's password field.")
    }

    static var showPassword: String {
        String(localized: "Show password", comment: "VoiceOver label of the eye button that shows the typed password.")
    }

    static var hidePassword: String {
        String(localized: "Hide password", comment: "VoiceOver label of the eye button that hides the typed password again.")
    }

    static var server: String {
        String(localized: "Server", comment: "Once the phone has signed in before, the one line naming the server it uses: this word, then the server, e.g. 'tracker.example.org'.")
    }

    static var change: String {
        String(localized: "Change", comment: "Button at the end of the server line that opens the server address field to type another.")
    }

    static var signIn: String {
        String(localized: "Sign in", comment: "The sign-in screen's button.")
    }

    static var signingIn: String {
        String(localized: "Signing in", comment: "The sign-in button while the server is being asked, beside a spinner.")
    }

    static var setByAgency: String {
        String(localized: "Set by your agency.", comment: "Under the server line when the agency's device management sets the server.")
    }

    static var managedServerInvalid: String {
        String(localized: "Your agency's server setting is not a valid address, so type it here.", comment: "Under the server field when the agency's device management set a server the app can't use.")
    }

    // MARK: Errors

    static var errorLabel: String {
        String(localized: "Error", comment: "VoiceOver: read before the error under a sign-in field, e.g. 'Error, Enter your email'.")
    }

    static func missing(_ field: SignInField) -> String {
        switch field {
        case .server:
            String(localized: "Enter the server address", comment: "Under the server field when it was left empty.")
        case .email:
            String(localized: "Enter your email", comment: "Under the email field when it was left empty.")
        case .password:
            String(localized: "Enter your password", comment: "Under the password field when it was left empty.")
        }
    }

    static var wrongEmailOrPassword: String {
        String(localized: "Wrong email or password. Try again.", comment: "Under the password field when the server turned the email and password down.")
    }

    static var checkTheAddress: String {
        String(localized: "Check the address", comment: "Under the server field when nothing answered at that address, beside the banner that says so.")
    }

    static func cannotReach(_ server: String) -> String {
        String(localized: "Can't reach \(server). Check your connection, or the server address.", comment: "Banner above the sign-in button when nothing answered. The server's address as the driver knows it, e.g. 'tracker.example.org'.")
    }

    static var couldNotSignIn: String {
        String(localized: "Couldn't sign in. Try again.", comment: "Banner above the sign-in button when the server answered, but not with a sign-in, such as a server error.")
    }

    static var tooManyTries: String {
        String(localized: "Too many tries. Wait a minute, then try again.", comment: "Banner above the sign-in button when the server refused for too many attempts; its window is one minute.")
    }

    static var useHTTPS: String {
        String(localized: "Use https://. Plain http:// is allowed only for localhost, 127.0.0.1 or a .local host.", comment: "Under the server field when the address is plain http to a host the app won't send a password to.")
    }
}
