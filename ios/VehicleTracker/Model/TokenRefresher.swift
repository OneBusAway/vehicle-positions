import Foundation
import OSLog

/// Trades the stored refresh token for a new access token, for every
/// `TrackerClient` the app makes.
///
/// One trade at a time, across the whole app. The server takes a second use of
/// a refresh token for theft and revokes every refresh token the driver holds,
/// so two requests refreshing together would sign the driver out. That covers
/// requests from different clients too: after a re-sign-in the trip's reporter
/// keeps the client it started with while the pickers get a new one, so
/// `AppContainer` makes one refresher and hands it to both.
///
/// Each trade starts from the token store, never from a token a caller
/// remembered. When the stored access token is no longer the one the caller
/// sent, another request has refreshed in the meantime, and the caller gets
/// the new token without a call to the server. Only within one sign-in,
/// though: once another driver has signed in, the caller's request is still
/// the driver's before, and is never sent with the new driver's token.
actor TokenRefresher {
    /// What the server said to a refresh token.
    nonisolated enum RefreshReply: Sendable, Equatable {
        case issued(AuthTokens)
        /// A 401: the token is unknown, expired, already used, or its driver
        /// was deactivated or deleted.
        case refused
        /// No answer, or one that says nothing about the token (a 5xx, a 429,
        /// a reply that is not a token pair).
        case unreachable(String)
    }

    /// Presents a refresh token to the server, without an access token.
    typealias Refresh = @Sendable (_ refreshToken: String) async -> RefreshReply

    /// What a request that got a 401 does next.
    nonisolated enum Outcome: Sendable, Equatable {
        /// Send the request again with this access token.
        case retry(String)
        /// The server refused the refresh token, or there is none to present:
        /// the 401 stands.
        case refused
        /// The server could not be reached or could not refresh right now.
        /// Both tokens are kept, so the next request tries again.
        case unreachable(String)
    }

    private let tokens: any TokenStoring
    private let now: @Sendable () -> Date
    private var inFlight: Task<Outcome, Never>?
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "token-refresher")

    init(tokens: any TokenStoring, now: @escaping @Sendable () -> Date = { Date() }) {
        self.tokens = tokens
        self.now = now
    }

    /// After a request sent with `failedToken`, from sign-in `signInID`, got
    /// a 401. A refused refresh token is dropped and the access token kept, so
    /// the 401 reaches the caller as it did before refresh: the driver is
    /// asked to sign in, and the trip's record stays.
    func afterUnauthorized(failedToken: String, signInID: UUID?, refresh: @escaping Refresh) async -> Outcome {
        // Nothing is awaited from reading the store to setting `inFlight`: at
        // an await the actor takes the next caller, which would find no
        // refresh running and start a second one.
        let stored: StoredToken?
        do {
            stored = try tokens.load()
        } catch {
            log.error("could not read the stored sign-in to refresh it: \(String(describing: error), privacy: .public)")
            return .unreachable("the stored sign-in could not be read")
        }
        guard let stored, stored.signInID == signInID else { return .refused }
        if stored.token != failedToken { return .retry(stored.token) }
        let task: Task<Outcome, Never>
        if let inFlight {
            task = inFlight
        } else {
            guard let refreshToken = stored.refreshToken else { return .refused }
            task = Task { await trade(refreshToken, refresh: refresh) }
            inFlight = task
        }
        let outcome = await task.value
        // Also when the refused refresh was an early one this request joined,
        // which keeps the token for requests whose access token still works.
        if outcome == .refused, let refreshToken = stored.refreshToken {
            dropRefreshToken(refreshToken)
        }
        return outcome
    }

    /// Whether `stored`'s access token is due a refresh before it expires.
    nonisolated func isRefreshDue(_ stored: StoredToken) -> Bool {
        stored.refreshDueAt.map { now() >= $0 } ?? false
    }

    /// Before a request goes out with `observedToken`, from sign-in
    /// `signInID`: the token to send it with, renewed when it is about to
    /// expire. An early refresh that fails or is refused never fails the
    /// request, which goes out with the token it has while that still works;
    /// the 401 after it expires is where a refused refresh token is dropped
    /// and a failure reported.
    func beforeExpiry(observedToken: String, signInID: UUID?, refresh: @escaping Refresh) async -> String {
        // As in `afterUnauthorized`, nothing is awaited until a refresh has
        // been joined or started.
        guard let stored = try? tokens.load(), stored.signInID == signInID else { return observedToken }
        // A token another request has just refreshed is not due.
        if stored.token != observedToken { return stored.token }
        let task: Task<Outcome, Never>
        if let inFlight {
            task = inFlight
        } else {
            guard isRefreshDue(stored), let refreshToken = stored.refreshToken else { return observedToken }
            task = Task { await trade(refreshToken, refresh: refresh) }
            inFlight = task
        }
        if case .retry(let renewed) = await task.value { return renewed }
        return observedToken
    }

    /// Presents `refreshToken` once and saves the pair the server hands back.
    private func trade(_ refreshToken: String, refresh: Refresh) async -> Outcome {
        defer { inFlight = nil }
        switch await refresh(refreshToken) {
        case .issued(let issued):
            save(issued, spending: refreshToken)
            return .retry(issued.accessToken)
        case .refused:
            log.warning("the server refused the refresh token")
            return .refused
        case .unreachable(let message):
            log.warning("could not refresh; keeping both tokens for the next request: \(message, privacy: .public)")
            return .unreachable(message)
        }
    }

    /// The server spent the old refresh token when it answered, so a pair that
    /// can't be saved costs the old one too: presented again, it would revoke
    /// every refresh token the driver holds. The requests waiting on this
    /// refresh still get the new access token.
    private func save(_ issued: AuthTokens, spending refreshToken: String) {
        do {
            // A sign-in or a sign-out while the refresh was out has replaced
            // the session that spent the token, and the newer one is kept.
            guard let spent = try tokens.load(), spent.refreshToken == refreshToken else { return }
            var renewed = StoredToken(issued, receivedAt: now())
            renewed.signInID = spent.signInID
            try tokens.save(renewed)
        } catch {
            log.error("could not save the refreshed tokens; signing out: \(String(describing: error), privacy: .public)")
            try? tokens.clear()
        }
    }

    /// Forgets a refused refresh token and keeps the access token, which works
    /// until it expires.
    private func dropRefreshToken(_ refreshToken: String) {
        do {
            guard var current = try tokens.load(), current.refreshToken == refreshToken else { return }
            current.refreshToken = nil
            try tokens.save(current)
        } catch {
            log.error("could not drop the refused refresh token: \(String(describing: error), privacy: .public)")
        }
    }
}
