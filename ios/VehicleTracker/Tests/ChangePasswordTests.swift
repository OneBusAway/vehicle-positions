import Foundation
import Testing
@testable import VehicleTracker

/// What Change password checks before anything is sent, and where each
/// answer shows.
@MainActor @Suite struct PasswordChangeTests {
    func tried(_ current: String = "old-secret", _ new: String = "new-secret", _ again: String? = nil) -> (PasswordChange, Bool) {
        var form = PasswordChange()
        let begun = form.begin(current: current, new: new, again: again ?? new)
        return (form, begun)
    }

    @Test func theButtonWaitsForAllThreeFields() {
        #expect(!PasswordChange.canSend(current: "", new: "new-secret", again: "new-secret"))
        #expect(!PasswordChange.canSend(current: "old-secret", new: "", again: "new-secret"))
        #expect(!PasswordChange.canSend(current: "old-secret", new: "new-secret", again: ""))
        #expect(PasswordChange.canSend(current: "old-secret", new: "new-secret", again: "new-secret"))
        let (form, begun) = tried("", "new-secret")
        #expect(!begun)
        #expect(form.said == nil, "a field left empty says nothing: the button can't be tapped")
    }

    /// As the server counts: Unicode scalars for the 8, UTF-8 bytes for
    /// bcrypt's 72.
    @Test(arguments: [
        ("1234567", PasswordChange.NewProblem.tooShort),
        ("12345678", nil),
        ("\u{E9}\u{E9}\u{E9}\u{E9}\u{E9}\u{E9}\u{E9}", .tooShort),
        // Four letters on screen, eight scalars, as the server counts runes.
        ("e\u{301}e\u{301}e\u{301}e\u{301}", nil),
        (String(repeating: "a", count: 72), nil),
        (String(repeating: "a", count: 73), .tooLong),
        (String(repeating: "\u{1F600}", count: 18), nil),
        (String(repeating: "\u{1F600}", count: 19), .tooLong),
    ])
    func theServersRules(new: String, problem: PasswordChange.NewProblem?) {
        #expect(PasswordChange.problem(with: new) == problem)
    }

    @Test func tooShortAndNoMatchShowTogether_andNothingIsSent() {
        let (form, begun) = tried("old-secret", "short", "shorter")
        #expect(!begun)
        #expect(!form.isChanging)
        #expect(form.error(for: .current) == nil)
        #expect(form.error(for: .new) == "Use at least 8 characters.")
        #expect(form.error(for: .again) == "The two new passwords don't match.")
        #expect(tried("old-secret", String(repeating: "a", count: 73)).0.error(for: .new) == "Use a shorter password.")
    }

    @Test func aGoodChangeIsUnderWay_andASecondTapSendsNothing() {
        var (form, begun) = tried()
        #expect(begun)
        #expect(form.isChanging)
        let second = form.begin(current: "old-secret", new: "new-secret", again: "new-secret")
        #expect(!second)
        form.succeeded()
        #expect(!form.isChanging)
    }

    func after(_ error: any Error) -> PasswordChange {
        var form = tried().0
        form.failed(error, server: "metro.example")
        return form
    }

    @Test func aWrongCurrentPasswordShowsUnderIt() {
        let form = after(APIError.status(422, message: "current password is wrong"))
        #expect(form.error(for: .current) == "That's not your current password.")
        #expect(form.banner == nil)
        #expect(!form.isChanging)
    }

    @Test func whatNoFieldIsToBlameForIsTheBanner() {
        #expect(after(APIError.status(429, message: "")).banner?.text == "Too many tries. Wait a minute, then try again.")
        #expect(after(APIError.status(404, message: "")).banner?.text == "Your agency's server can't change passwords yet.")
        #expect(after(APIError.transport("offline")).banner?.text == "Can't reach metro.example. Check your connection.")
        #expect(after(APIError.transport("offline")).banner?.symbol == "icloud.slash.fill")
        for error in [APIError.status(500, message: ""), .status(403, message: ""), .status(400, message: ""),
                      .status(409, message: ""), .decoding("not json")] {
            let form = after(error)
            #expect(form.banner?.text == "Couldn't change your password. Try again.")
            #expect(PasswordChangeField.allCases.allSatisfy { form.error(for: $0) == nil })
        }
    }

    @Test func typingInAFieldTakesBackOnlyItsOwnLine() {
        var form = tried("old-secret", "short", "shorter").0
        form.edited(.again)
        #expect(form.error(for: .again) == nil)
        #expect(form.error(for: .new) == "Use at least 8 characters.")
        form.edited(.current)
        #expect(form.error(for: .new) != nil)
        form.edited(.new)
        #expect(form.error(for: .new) == nil)

        var wrong = after(APIError.status(422, message: ""))
        wrong.edited(.new)
        wrong.edited(.again)
        #expect(wrong.error(for: .current) != nil)
        wrong.edited(.current)
        #expect(wrong.error(for: .current) == nil)
    }

    @Test func aNewPasswordTypedAgainTakesBackTheMismatchToo() {
        var form = tried("old-secret", "new-secret", "new-secrex").0
        #expect(form.noMatch)
        form.edited(.new)
        #expect(!form.noMatch)
    }

    @Test func anyTypingTakesBackTheBanner() {
        var form = after(APIError.transport("offline"))
        form.edited(.again)
        #expect(form.banner == nil)
    }

    @Test func leavingNewPasswordSaysWhatIsWrongWithIt() {
        var form = PasswordChange()
        form.leftNew("")
        #expect(form.error(for: .new) == nil, "left empty, it is not yet wrong")
        form.leftNew("short")
        #expect(form.error(for: .new) == "Use at least 8 characters.")
        form.leftNew("long enough")
        #expect(form.error(for: .new) == nil)
    }

    @Test func voiceOverHearsTheBannerFirst_elseTheFirstLine() {
        #expect(tried("old-secret", "short", "shorter").0.said == "Use at least 8 characters.")
        #expect(tried("old-secret", "new-secret", "new-secrex").0.said == "The two new passwords don't match.")
        #expect(after(APIError.status(429, message: "")).said == "Too many tries. Wait a minute, then try again.")
    }

    @Test func noWordSaysBus() {
        let words = [ChangePasswordText.title, ChangePasswordText.current, ChangePasswordText.new, ChangePasswordText.again,
                     ChangePasswordText.rule, ChangePasswordText.othersSignedOut, ChangePasswordText.wrongCurrent,
                     ChangePasswordText.tooShort, ChangePasswordText.tooLong, ChangePasswordText.noMatch,
                     ChangePasswordText.changing, ChangePasswordText.cannotReach("metro.example"),
                     ChangePasswordText.olderServer, ChangePasswordText.failed, ChangePasswordText.changed,
                     ChangePasswordText.changedDetail, SettingsText.accountSection]
        #expect(words.allSatisfy { !$0.localizedCaseInsensitiveContains("bus") })
    }
}

@MainActor @Suite struct ChangePasswordSessionTests {
    let api = FakeTrackerAPI()

    func session() -> TripSession {
        let defaults = UserDefaults(suiteName: "ChangePasswordSessionTests-\(UUID().uuidString)")!
        return TripSession(settings: AppSettings(defaults: defaults), tokens: InMemoryTokenStore(), store: InMemoryActiveTripStore(),
                           locations: FakeLocationSource(), access: FakeLocationAccess(), makeAPI: { [api] _, _ in api })
    }

    func signedIn() async throws -> TripSession {
        let session = session()
        try await session.signIn(serverURL: URL(string: "https://metro.example")!, email: "d@test.com", password: "pw")
        return session
    }

    @Test func theChangeGoesToTheServerSignedIn() async throws {
        try await signedIn().changePassword(current: "old-secret", new: "new-secret")
        #expect(api.passwordChanges.map { [$0.0, $0.1] } == [["old-secret", "new-secret"]])
    }

    @Test func whatTheServerSaidReachesTheScreen() async throws {
        api.passwordError = APIError.status(422, message: "current password is wrong")
        let session = try await signedIn()
        await #expect(throws: APIError.status(422, message: "current password is wrong")) {
            try await session.changePassword(current: "wrong", new: "new-secret")
        }
    }

    @Test func signedOutThereIsNothingToChange() async {
        await #expect(throws: TripSessionError.notSignedIn) {
            try await session().changePassword(current: "old-secret", new: "new-secret")
        }
        #expect(api.passwordChanges.isEmpty)
    }
}

/// A sequence of answers for `/api/v1/auth/password`, one per request.
nonisolated final class PasswordAnswers: @unchecked Sendable {
    private let lock = NSLock()
    private var left: [Int]

    init(_ codes: [Int]) { left = codes }

    func next() -> Int {
        lock.withLock { left.isEmpty ? 204 : left.removeFirst() }
    }
}

extension TrackerClientTests {
    /// `/auth/password` answers `codes` in turn; `/auth/refresh` gives a2 and r2.
    func servePassword(_ codes: [Int]) {
        let answers = PasswordAnswers(codes)
        StubURLProtocol.reset { req in
            if req.url!.path == "/api/v1/auth/refresh" {
                return (200, Data(#"{"access_token":"a2","refresh_token":"r2","expires_in":900}"#.utf8))
            }
            let code = answers.next()
            return (code, code == 204 ? Data() : Data(#"{"error":"refused"}"#.utf8))
        }
    }

    func passwordBody(_ request: StubURLProtocol.Recorded) throws -> [String: String] {
        let body = try #require(request.body)
        return try #require(try JSONSerialization.jsonObject(with: body) as? [String: String])
    }

    @Test func aPasswordChangeSendsBothPasswordsAndThisPhonesRefreshToken() async throws {
        servePassword([204])
        let store = InMemoryTokenStore(signedIn)
        try await refreshingClient(store).changePassword(current: "old-secret", new: "new-secret")

        let sent = requests
        try #require(sent.count == 1)
        #expect(sent[0].method == "POST")
        #expect(sent[0].url.path == "/api/v1/auth/password")
        #expect(sent[0].headers["Authorization"] == "Bearer a1")
        #expect(sent[0].headers["Content-Type"] == "application/json")
        #expect(try passwordBody(sent[0]) == ["current_password": "old-secret", "new_password": "new-secret", "refresh_token": "r1"])
        #expect(try store.load()?.refreshToken == "r1", "the server keeps it good, so it is kept")
    }

    /// The token was spent on the way, by a refresh for another request.
    @Test func a409IsRefreshedAndTheChangeSentOnceMoreWithTheNewToken() async throws {
        servePassword([409, 204])
        let store = InMemoryTokenStore(signedIn)
        try await refreshingClient(store).changePassword(current: "old-secret", new: "new-secret")

        let sent = requests
        try #require(sent.map(\.url.path) == ["/api/v1/auth/password", "/api/v1/auth/refresh", "/api/v1/auth/password"])
        #expect(try passwordBody(sent[1]) == ["refresh_token": "r1"])
        #expect(try passwordBody(sent[2])["refresh_token"] == "r2")
        #expect(sent[2].headers["Authorization"] == "Bearer a2")
    }

    @Test func aSecond409IsTheCallers() async throws {
        servePassword([409, 409])
        let client = try refreshingClient(InMemoryTokenStore(signedIn))
        await #expect(throws: APIError.status(409, message: "refused")) {
            try await client.changePassword(current: "old-secret", new: "new-secret")
        }
        #expect(requests.map(\.url.path) == ["/api/v1/auth/password", "/api/v1/auth/refresh", "/api/v1/auth/password"])
    }

    @Test func aWrongCurrentPasswordIsNotRefreshed() async throws {
        servePassword([422])
        let client = try refreshingClient(InMemoryTokenStore(signedIn))
        await #expect(throws: APIError.status(422, message: "refused")) {
            try await client.changePassword(current: "wrong", new: "new-secret")
        }
        #expect(requests.map(\.url.path) == ["/api/v1/auth/password"])
    }

    /// A refresh another request has out may be spending the token, so the
    /// change waits for it and names the new one.
    @Test func aRefreshAlreadyOutIsWaitedFor() async throws {
        servePassword([204])
        let store = ScriptedTokenStore(signedIn)
        let now = now
        let refresher = TokenRefresher(tokens: store, now: { now })
        let held = ScriptedRefresh()
        async let first = refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: held.call)
        #expect(await eventually { held.presented.count == 1 })

        let client = try refreshingClient(store, refresher: refresher)
        async let change: Void = client.changePassword(current: "old-secret", new: "new-secret")
        #expect(await eventually { store.loads == 2 })
        #expect(requests.isEmpty, "nothing goes while the refresh is out")
        held.answer(.issued(AuthTokens(accessToken: "a2", refreshToken: "r2", expiresIn: 900)))

        try await change
        #expect(await first == .retry("a2"))
        let sent = try #require(requests.last)
        #expect(sent.url.path == "/api/v1/auth/password")
        #expect(try passwordBody(sent)["refresh_token"] == "r2")
    }

    /// With no refresh token this sign-in can't outlive the change.
    @Test func withNoRefreshTokenTheSignInIsOver_andNothingIsSent() async throws {
        servePassword([204])
        var stored = signedIn
        stored.refreshToken = nil
        let client = try refreshingClient(InMemoryTokenStore(stored))
        await #expect(throws: APIError.status(401, message: "")) {
            try await client.changePassword(current: "old-secret", new: "new-secret")
        }
        #expect(requests.isEmpty)
    }
}
