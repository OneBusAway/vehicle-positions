import Foundation
import Testing
@testable import VehicleTracker

@Suite struct TokenRefresherTests {
    let now = Date(timeIntervalSince1970: 1_792_476_000)
    let renewed = AuthTokens(accessToken: "a2", refreshToken: "r2", expiresIn: 900)
    let signIn = UUID()

    /// Signed in with a1 and r1, the access token just expired.
    var signedIn: StoredToken {
        StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-900), refreshToken: "r1", expiresAt: now, signInID: signIn)
    }

    /// What a refresh of `signedIn` saves.
    var renewedInThisSignIn: StoredToken {
        var token = StoredToken(renewed, receivedAt: now)
        token.signInID = signIn
        return token
    }

    func refresher(_ tokens: any TokenStoring) -> TokenRefresher {
        let now = now
        return TokenRefresher(tokens: tokens, now: { now })
    }

    /// The race from #166: two requests that get a 401 together. The server
    /// takes a second use of r1 for theft, so it must be presented once.
    @Test func twoRequestsTurnedAwayTogetherMakeOneRefresh() async throws {
        let store = ScriptedTokenStore(signedIn)
        let refresher = refresher(store)
        let refresh = ScriptedRefresh()

        async let first = refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call)
        async let second = refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call)
        // Each reads the store, then starts or joins the refresh with no
        // await in between, so after two reads both are in.
        #expect(await eventually { store.loads == 2 && !refresh.presented.isEmpty })
        refresh.answer(.issued(renewed))

        #expect(await first == .retry("a2"))
        #expect(await second == .retry("a2"))
        #expect(refresh.presented == ["r1"])
        #expect(try store.load() == StoredToken(renewed, receivedAt: now))
    }

    @Test func aRequestThatMissedARefreshGetsTheNewTokenWithoutAnother() async throws {
        let store = ScriptedTokenStore(renewedInThisSignIn)
        let refresh = ScriptedRefresh(answering: .issued(AuthTokens(accessToken: "a3", refreshToken: "r3", expiresIn: 900)))
        #expect(await refresher(store).afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .retry("a2"))
        #expect(refresh.presented.isEmpty)
    }

    /// A refresh renews the sign-in it was made for, so a request still out
    /// with the old access token is sent again with the new one.
    @Test func aRefreshStaysInTheSameSignIn() async throws {
        let store = ScriptedTokenStore(signedIn)
        let refresher = refresher(store)
        let refresh = ScriptedRefresh(answering: .issued(renewed))
        #expect(await refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .retry("a2"))
        #expect(try store.load()?.signInID == signIn)

        #expect(await refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .retry("a2"))
        #expect(refresh.presented == ["r1"])
    }

    @Test func aRefusedRefreshTokenIsDroppedAndTheAccessTokenKept() async throws {
        let store = ScriptedTokenStore(signedIn)
        let refresher = refresher(store)
        let refresh = ScriptedRefresh(answering: .refused)
        #expect(await refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .refused)
        var dropped = signedIn
        dropped.refreshToken = nil
        #expect(try store.load() == dropped)

        #expect(await refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .refused)
        #expect(refresh.presented == ["r1"], "with no refresh token left there is nothing to present")
    }

    @Test func anUnreachableServerKeepsBothTokens() async throws {
        let store = ScriptedTokenStore(signedIn)
        let refresh = ScriptedRefresh(answering: .unreachable("offline"))
        #expect(await refresher(store).afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .unreachable("offline"))
        #expect(try store.load() == signedIn)
    }

    /// The server spent r1 when it answered: kept, it would be presented
    /// again and revoke every refresh token the driver holds.
    @Test func aPairThatCannotBeSavedSignsOutButServesTheRequest() async throws {
        let store = ScriptedTokenStore(signedIn)
        store.failsSave = true
        let refresher = refresher(store)
        let refresh = ScriptedRefresh(answering: .issued(renewed))
        #expect(await refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call) == .retry("a2"))
        #expect(try store.load() == nil)

        #expect(await refresher.afterUnauthorized(failedToken: "a2", signInID: signIn, refresh: refresh.call) == .refused)
        #expect(refresh.presented == ["r1"])
    }

    @Test func aStoreThatCannotBeReadIsReportedAsUnreachable() async {
        let store = ScriptedTokenStore(signedIn)
        store.failsLoad = true
        let refresh = ScriptedRefresh(answering: .issued(renewed))
        #expect(await refresher(store).afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call)
            == .unreachable("the stored sign-in could not be read"))
        #expect(refresh.presented.isEmpty)
    }

    /// The driver signs in again while a refresh is out: its late reply must
    /// not overwrite the new session.
    @Test func aSignInDuringARefreshIsKept() async throws {
        let store = ScriptedTokenStore(signedIn)
        let refresh = ScriptedRefresh()
        async let outcome = refresher(store).afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call)
        #expect(await eventually { refresh.presented.count == 1 })
        let newSession = StoredToken(token: "b1", issuedAt: now, refreshToken: "rb1", expiresAt: now.addingTimeInterval(900))
        try? store.save(newSession)
        refresh.answer(.issued(renewed))

        #expect(await outcome == .retry("a2"))
        #expect(try store.load() == newSession)
    }

    @Test func aSignInDuringARefusedRefreshKeepsItsRefreshToken() async throws {
        let store = ScriptedTokenStore(signedIn)
        let refresh = ScriptedRefresh()
        async let outcome = refresher(store).afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call)
        #expect(await eventually { refresh.presented.count == 1 })
        let newSession = StoredToken(token: "b1", issuedAt: now, refreshToken: "rb1", expiresAt: now.addingTimeInterval(900))
        try? store.save(newSession)
        refresh.answer(.refused)

        #expect(await outcome == .refused)
        #expect(try store.load() == newSession)
    }

    // MARK: Before expiry

    /// Signed in with a1 and r1, thirty seconds from expiry: a refresh is due.
    var aboutToExpire: StoredToken {
        StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-870), refreshToken: "r1", expiresAt: now.addingTimeInterval(30),
                    signInID: signIn)
    }

    @Test func aRefreshIsDueFromItsDueTimeOn() {
        let refresher = refresher(InMemoryTokenStore())
        let dueNow = StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-840), refreshToken: "r1", expiresAt: now.addingTimeInterval(60))
        let dueInASecond = StoredToken(token: "a1", issuedAt: now.addingTimeInterval(-839), refreshToken: "r1", expiresAt: now.addingTimeInterval(61))
        #expect(refresher.isRefreshDue(dueNow))
        #expect(!refresher.isRefreshDue(dueInASecond))
    }

    @Test func requestsAboutToGoOutTogetherMakeOneEarlyRefresh() async throws {
        let store = ScriptedTokenStore(aboutToExpire)
        let refresher = refresher(store)
        let refresh = ScriptedRefresh()

        async let first = refresher.beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call)
        async let second = refresher.beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call)
        #expect(await eventually { store.loads == 2 && !refresh.presented.isEmpty })
        refresh.answer(.issued(renewed))

        #expect(await first == "a2")
        #expect(await second == "a2")
        #expect(refresh.presented == ["r1"])
    }

    @Test func aRequestThatMissedAnEarlyRefreshGoesOutWithTheNewToken() async throws {
        let store = ScriptedTokenStore(renewedInThisSignIn)
        let refresh = ScriptedRefresh(answering: .refused)
        #expect(await refresher(store).beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call) == "a2")
        #expect(refresh.presented.isEmpty)
    }

    @Test func aTokenWithTimeLeftIsNotRefreshedEarly() async {
        var withTimeLeft = aboutToExpire
        withTimeLeft.expiresAt = now.addingTimeInterval(900)
        let refresh = ScriptedRefresh(answering: .issued(renewed))
        #expect(await refresher(ScriptedTokenStore(withTimeLeft)).beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call) == "a1")
        #expect(refresh.presented.isEmpty)
    }

    /// A request about to go out for the driver before keeps that driver's
    /// token after the next driver has signed in.
    @Test func anEarlyRefreshNeverHandsOutTheNextDriversToken() async throws {
        let nextDriver = StoredToken(AuthTokens(accessToken: "b1", refreshToken: "rb1", expiresIn: 900), receivedAt: now)
        let store = ScriptedTokenStore(nextDriver)
        let refresh = ScriptedRefresh(answering: .issued(renewed))
        #expect(await refresher(store).beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call) == "a1")
        #expect(refresh.presented.isEmpty)
        #expect(try store.load() == nextDriver)
    }

    /// The token still works, so the request goes out with it; the refresh
    /// token is dropped only by the 401 after expiry.
    @Test func aRefusedEarlyRefreshKeepsTheRefreshToken() async throws {
        let store = ScriptedTokenStore(aboutToExpire)
        let refresh = ScriptedRefresh(answering: .refused)
        #expect(await refresher(store).beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call) == "a1")
        #expect(try store.load() == aboutToExpire)
    }

    /// A request turned away while an early refresh is out joins it. If the
    /// server refuses that refresh, the 401 drops the refresh token as it would
    /// after a refresh of its own, so the next request doesn't present it again.
    @Test func a401ThatJoinsARefusedEarlyRefreshDropsTheRefreshToken() async throws {
        let store = ScriptedTokenStore(aboutToExpire)
        let refresher = refresher(store)
        let refresh = ScriptedRefresh()
        async let early = refresher.beforeExpiry(observedToken: "a1", signInID: signIn, refresh: refresh.call)
        #expect(await eventually { refresh.presented.count == 1 })
        async let turnedAway = refresher.afterUnauthorized(failedToken: "a1", signInID: signIn, refresh: refresh.call)
        #expect(await eventually { store.loads == 2 })
        refresh.answer(.refused)

        #expect(await early == "a1")
        #expect(await turnedAway == .refused)
        #expect(refresh.presented == ["r1"])
        var dropped = aboutToExpire
        dropped.refreshToken = nil
        #expect(try store.load() == dropped)
    }
}
