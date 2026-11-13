import Foundation

/// The sign-in fields, in the order they are checked.
nonisolated enum SignInField: Hashable, CaseIterable {
    case server, email, password
}

/// Why a sign-in did not go through. Each is shown under the field it is
/// about or, with no field to blame, in a banner above the button.
nonisolated enum SignInFailure: Error, Equatable {
    /// The field holds nothing, or only spaces.
    case missing(SignInField)
    /// A 401: the email and the password don't go together.
    case invalidCredentials
    /// Nothing answered at the server, or what was typed is not an http(s)
    /// address at all. The server as the banner names it (`serverLabel`).
    case unreachable(String)
    /// A 429: the server's one-minute sign-in window is used up.
    case tooManyTries
    /// Plain http to a host other than this phone or the local network
    /// (`ServerURLPolicy`).
    case insecure
    /// The server answered, but not with a sign-in: a 5xx, a 403 or a 409,
    /// a reply that can't be read, or a sign-in the phone couldn't keep.
    case serverError
}

/// A banner above the button, for a failure no one field is to blame for.
nonisolated struct SignInBanner: Equatable {
    let symbol: String
    let text: String
}

/// Everything Sign in and Sign in again decide, apart from the screen: what
/// is wrong with what was typed, where each failure shows, and when the
/// server field opens. A port of Android's `LoginViewModel`, `fieldErrorRes`
/// and `bannerOf`. The screens keep one in `@State` and call it.
nonisolated struct SignInForm: Equatable {
    /// Signing in again during a trip, which stays open meanwhile.
    let isAgain: Bool
    private(set) var failure: SignInFailure?
    private(set) var isSigningIn = false
    /// The driver asked to change the saved server, or a failure is about
    /// it. Once open it stays open.
    private(set) var changingServer = false

    init(isAgain: Bool = false) {
        self.isAgain = isAgain
    }

    /// Whether the server shows as a field to type in, rather than as the
    /// one line of the server the phone knows. A managed server is never
    /// typed over; one the app can't use is ignored, so the field opens.
    func showsServerField(savedServer: URL?, managedInvalid: Bool) -> Bool {
        savedServer == nil || changingServer || managedInvalid
    }

    /// Checks what was typed and, if it can go, marks the form as signing in
    /// and returns the address to sign in to. Nil while a sign-in is already
    /// out (a second tap), or when a check failed, which is then `failure`.
    mutating func begin(server: String, email: String, password: String) -> URL? {
        guard !isSigningIn else { return nil }
        switch Self.check(server: server, email: email, password: password) {
        case .success(let url):
            failure = nil
            isSigningIn = true
            return url
        case .failure(let failure):
            fail(failure)
            return nil
        }
    }

    /// What `begin` checks before anything is sent: a blank field, in the
    /// order server, email, password; then an address that is not an http or
    /// https one (nothing could answer at it); then plain http to a host the
    /// app won't send a token to.
    static func check(server: String, email: String, password: String) -> Result<URL, SignInFailure> {
        if isBlank(server) { return .failure(.missing(.server)) }
        if isBlank(email) { return .failure(.missing(.email)) }
        if isBlank(password) { return .failure(.missing(.password)) }
        guard let url = address(typed: server) else { return .failure(.unreachable(serverLabel(server))) }
        guard ServerURLPolicy.isAllowed(url) else { return .failure(.insecure) }
        return .success(url)
    }

    /// The address to sign in to, from what was typed: trimmed, and over
    /// https when no scheme was typed, since that is how a driver is usually
    /// told the address (Android's `serverAddressOf`). Nil when it is not an
    /// http or https address at all. With https put in front, "https//host"
    /// would go to a server called "https", and an email typed in the wrong
    /// field to its domain with the password, so neither is an address.
    static func address(typed: String) -> URL? {
        let trimmed = typed.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasScheme = trimmed.contains("://")
        guard let url = URL(string: hasScheme ? trimmed : "https://\(trimmed)"),
              let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = url.host()?.lowercased(), !host.isEmpty else { return nil }
        if !hasScheme, host == "http" || host == "https" || url.user() != nil { return nil }
        return url
    }

    /// The sign-in that `begin` let through came back with `error`.
    mutating func failed(_ error: any Error, server: String) {
        isSigningIn = false
        switch error {
        case APIError.status(401, _): fail(.invalidCredentials)
        case APIError.status(429, _): fail(.tooManyTries)
        case APIError.transport: fail(.unreachable(Self.serverLabel(server)))
        case APIError.insecureURL: fail(.insecure)
        default: fail(.serverError)
        }
    }

    mutating func succeeded() {
        isSigningIn = false
    }

    /// An error is about what was typed, so it goes once the driver types
    /// again, in any field. Nothing else clears it.
    mutating func edited() {
        failure = nil
    }

    mutating func changeServer() {
        changingServer = true
    }

    // A failure under the server field opens it on Sign in, so the error has
    // a field to sit under. Not on Sign in again: the open trip is on that
    // server, so the banner alone names it.
    private mutating func fail(_ failure: SignInFailure) {
        self.failure = failure
        if !isAgain && fieldError(for: .server) != nil {
            changingServer = true
        }
    }

    // MARK: Where each failure shows

    /// The error under `field`, if the failure is about it.
    func fieldError(for field: SignInField) -> String? {
        switch (failure, field) {
        case (.missing(let missing), _) where missing == field: SignInText.missing(field)
        case (.invalidCredentials, .password): SignInText.wrongEmailOrPassword
        case (.unreachable, .server): SignInText.checkTheAddress
        case (.insecure, .server): SignInText.useHTTPS
        default: nil
        }
    }

    /// The banner above the button, for a failure that is about no one field,
    /// or about the server as a whole.
    var banner: SignInBanner? {
        switch failure {
        case .unreachable(let server): SignInBanner(symbol: "icloud.slash.fill", text: SignInText.cannotReach(server))
        case .serverError: SignInBanner(symbol: "exclamationmark.circle.fill", text: SignInText.couldNotSignIn)
        case .tooManyTries: SignInBanner(symbol: "exclamationmark.circle.fill", text: SignInText.tooManyTries)
        case .missing, .invalidCredentials, .insecure, nil: nil
        }
    }

    /// The field that takes focus after a failure: only an empty one, as on
    /// Android. A wrong password is left as typed, unfocused.
    var focus: SignInField? {
        if case .missing(let field) = failure { return field }
        return nil
    }

    /// The server as the driver knows it: "tracker.example.org", with its
    /// port when it isn't the scheme's own (80 for http, 443 for https).
    /// Anything that isn't an http(s) address shows as typed, trimmed
    /// (Android's `serverLabel`).
    static func serverLabel(_ text: String) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = url.host()?.lowercased(), !host.isEmpty else {
            return trimmed
        }
        guard let port = url.port, port != (scheme == "http" ? 80 : 443) else { return host }
        return "\(host):\(port)"
    }

    private static func isBlank(_ text: String) -> Bool {
        text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}
