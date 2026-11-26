import Foundation

/// Change password's fields, in order.
nonisolated enum PasswordChangeField: Hashable, CaseIterable {
    case current, new, again
}

/// Everything Change password decides apart from the screen: what is wrong
/// with what was typed, the line under each field, and the banner for a
/// failure no field is to blame for. The screen keeps one in `@State`, as
/// Sign in keeps its `SignInForm`.
nonisolated struct PasswordChange: Equatable {
    /// The server's rules (`minPasswordLength`, `maxPasswordBytes`): at least
    /// 8 characters, counted as it counts them, by Unicode scalar, and at most
    /// 72 bytes of UTF-8, bcrypt's limit.
    static let minLength = 8
    static let maxBytes = 72

    nonisolated enum NewProblem: Equatable {
        case tooShort, tooLong
    }

    nonisolated enum Failure: Equatable {
        /// Nothing answered. The server as the banner names it (`SignInForm.serverLabel`).
        case unreachable(String)
        /// A 429: wrong passwords count with the account's failed sign-ins.
        case tooManyTries
        /// A 404: the server is older than the change.
        case olderServer
        /// Any other answer: a 5xx, a 403, a 400, or a second 409.
        case failed
    }

    private(set) var newProblem: NewProblem?
    private(set) var noMatch = false
    private(set) var wrongCurrent = false
    private(set) var failure: Failure?
    private(set) var isChanging = false

    /// Change password can be tapped once all three hold something.
    static func canSend(current: String, new: String, again: String) -> Bool {
        !current.isEmpty && !new.isEmpty && !again.isEmpty
    }

    static func problem(with new: String) -> NewProblem? {
        if new.unicodeScalars.count < minLength { return .tooShort }
        if new.utf8.count > maxBytes { return .tooLong }
        return nil
    }

    /// Leaving New password says at once what is wrong with it.
    mutating func leftNew(_ new: String) {
        guard !new.isEmpty else { return }
        newProblem = Self.problem(with: new)
    }

    /// Checks what was typed and, if it can go, marks the change as under
    /// way. False while one is out, and when a check failed, which then shows.
    mutating func begin(current: String, new: String, again: String) -> Bool {
        guard !isChanging, Self.canSend(current: current, new: new, again: again) else { return false }
        newProblem = Self.problem(with: new)
        noMatch = new != again
        wrongCurrent = false
        failure = nil
        guard newProblem == nil, !noMatch else { return false }
        isChanging = true
        return true
    }

    /// The change `begin` let through came back with `error`. Not for a 401,
    /// which ends the session (`TripSession.handleIfUnauthorized`).
    mutating func failed(_ error: any Error, server: String) {
        isChanging = false
        switch error {
        case APIError.status(422, _): wrongCurrent = true
        case APIError.status(429, _): failure = .tooManyTries
        case APIError.status(404, _): failure = .olderServer
        case APIError.transport: failure = .unreachable(server)
        default: failure = .failed
        }
    }

    mutating func succeeded() {
        isChanging = false
    }

    /// A line under a field is about what was typed there, so it goes once
    /// that field is typed in again. The banner goes with any edit.
    mutating func edited(_ field: PasswordChangeField) {
        failure = nil
        switch field {
        case .current:
            wrongCurrent = false
        case .new:
            newProblem = nil
            noMatch = false
        case .again:
            noMatch = false
        }
    }

    /// The line under `field`, if something is wrong with it.
    func error(for field: PasswordChangeField) -> String? {
        switch field {
        case .current: wrongCurrent ? ChangePasswordText.wrongCurrent : nil
        case .new: newProblem.map { $0 == .tooShort ? ChangePasswordText.tooShort : ChangePasswordText.tooLong }
        case .again: noMatch ? ChangePasswordText.noMatch : nil
        }
    }

    /// The banner above the fields, for a failure no field is to blame for.
    var banner: SignInBanner? {
        switch failure {
        case .unreachable(let server): SignInBanner(symbol: "icloud.slash.fill", text: ChangePasswordText.cannotReach(server))
        case .tooManyTries: SignInBanner(symbol: "exclamationmark.circle.fill", text: SignInText.tooManyTries)
        case .olderServer: SignInBanner(symbol: "exclamationmark.circle.fill", text: ChangePasswordText.olderServer)
        case .failed: SignInBanner(symbol: "exclamationmark.circle.fill", text: ChangePasswordText.failed)
        case nil: nil
        }
    }

    /// What VoiceOver says once a change or a check fails: the banner, or
    /// the first line under a field.
    var said: String? {
        banner?.text ?? PasswordChangeField.allCases.lazy.compactMap(error(for:)).first
    }
}
