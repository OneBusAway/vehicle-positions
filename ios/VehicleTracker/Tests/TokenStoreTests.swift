import Foundation
import Security
import Testing
@testable import VehicleTracker

@Suite struct TokenStoreTests {
    let issued = Date(timeIntervalSince1970: 1_000_000)

    /// The JSON a build before refresh wrote: no refresh token, no expiry.
    let previousBuildJSON = #"{"token":"jwt","issuedAt":"2026-10-20T06:00:00Z"}"#
    var previousBuildToken: StoredToken {
        StoredToken(token: "jwt", issuedAt: Date(timeIntervalSince1970: 1_792_476_000), signInID: nil)
    }

    @Test func aRefreshTokenKeepsTheDriverSignedInPastExpiry() {
        let t = StoredToken(token: "a1", issuedAt: issued, refreshToken: "r1", expiresAt: issued.addingTimeInterval(900))
        #expect(t.isSignedIn(now: issued.addingTimeInterval(901)))
    }

    @Test func withoutARefreshTokenTheAccessTokenLastsUntilItsExpiry() {
        let t = StoredToken(token: "a1", issuedAt: issued, expiresAt: issued.addingTimeInterval(900))
        #expect(t.isSignedIn(now: issued.addingTimeInterval(899)))
        #expect(!t.isSignedIn(now: issued.addingTimeInterval(900)))
    }

    @Test func aPreviousBuildsTokenLastsTwentyFourHoursFromSignIn() {
        let t = StoredToken(token: "jwt", issuedAt: issued)
        #expect(t.isSignedIn(now: issued.addingTimeInterval(24 * 3600 - 1)))
        #expect(!t.isSignedIn(now: issued.addingTimeInterval(24 * 3600)))
    }

    @Test func aPreviousBuildsTokenDecodes() throws {
        let decoded = try JSONCoding.decoder.decode(StoredToken.self, from: Data(previousBuildJSON.utf8))
        #expect(decoded == previousBuildToken)
        #expect(decoded.signInID == nil)
    }

    /// Two sign-ins, even with the same reply, are told apart by their ids
    /// alone.
    @Test func eachSignInHasItsOwnID() {
        let reply = AuthTokens(accessToken: "a1", refreshToken: "r1", expiresIn: 900)
        let first = StoredToken(reply, receivedAt: issued)
        let second = StoredToken(reply, receivedAt: issued)
        #expect(first.signInID != nil)
        #expect(first.signInID != second.signInID)
        #expect(first == second)
    }

    @Test func expiryIsTimedFromTheReply() {
        let t = StoredToken(AuthTokens(accessToken: "a1", refreshToken: "r1", expiresIn: 120), receivedAt: issued)
        #expect(t == StoredToken(token: "a1", issuedAt: issued, refreshToken: "r1", expiresAt: issued.addingTimeInterval(120)))
    }

    @Test func inMemoryRoundTrip() throws {
        let store = InMemoryTokenStore()
        #expect(try store.load() == nil)
        let t = StoredToken(token: "abc", issuedAt: Date(timeIntervalSince1970: 5))
        try store.save(t)
        #expect(try store.load() == t)
        try store.clear()
        #expect(try store.load() == nil)
    }

    /// Runs in the app-hosted test bundle, which has a signed host and so a
    /// usable Keychain on the simulator.
    @Test func keychainRoundTrip() throws {
        let store = KeychainTokenStore(service: "org.onebusaway.vehicletracker.tests", account: "token-\(UUID().uuidString)")
        defer { try? store.clear() }
        #expect(try store.load() == nil)
        let t = StoredToken(token: "jwt", issuedAt: Date(timeIntervalSince1970: 1_700_000_000))
        try store.save(t)
        #expect(try store.load() == t)
        let t2 = StoredToken(token: "jwt2", issuedAt: Date(timeIntervalSince1970: 1_700_000_100))
        try store.save(t2)
        #expect(try store.load() == t2, "save replaces in place")
        try store.clear()
        #expect(try store.load() == nil)
    }

    @Test func keychainKeepsTheRefreshTokenExpiryAndSignIn() throws {
        let store = KeychainTokenStore(service: "org.onebusaway.vehicletracker.tests", account: "token-\(UUID().uuidString)")
        defer { try? store.clear() }
        let t = StoredToken(token: "a1", issuedAt: Date(timeIntervalSince1970: 1_700_000_000),
                            refreshToken: "r1", expiresAt: Date(timeIntervalSince1970: 1_700_000_900))
        try store.save(t)
        #expect(try store.load() == t)
        #expect(try store.load()?.signInID == t.signInID)
    }

    /// The store deletes an item it cannot decode, so a field the previous
    /// build did not write must not be required: the update would sign every
    /// driver out.
    @Test func keychainLoadsAPreviousBuildsItem() throws {
        let service = "org.onebusaway.vehicletracker.tests"
        let account = "token-\(UUID().uuidString)"
        let store = KeychainTokenStore(service: service, account: account)
        defer { try? store.clear() }
        let added = SecItemAdd([
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: Data(previousBuildJSON.utf8),
        ] as CFDictionary, nil)
        try #require(added == errSecSuccess)
        #expect(try store.load() == previousBuildToken)
        #expect(try store.load()?.signInID == nil)
    }

    @Test func aRefreshIsDueAMinuteBeforeExpiry() {
        let day = StoredToken(token: "a1", issuedAt: issued, refreshToken: "r1", expiresAt: issued.addingTimeInterval(24 * 3600))
        #expect(day.refreshDueAt == issued.addingTimeInterval(24 * 3600 - 60))
        let twoMinutes = StoredToken(token: "a1", issuedAt: issued, refreshToken: "r1", expiresAt: issued.addingTimeInterval(120))
        #expect(twoMinutes.refreshDueAt == issued.addingTimeInterval(60))
    }

    @Test func aShortLivedTokenIsRefreshedHalfwayThrough() {
        let t = StoredToken(token: "a1", issuedAt: issued, refreshToken: "r1", expiresAt: issued.addingTimeInterval(60))
        #expect(t.refreshDueAt == issued.addingTimeInterval(30))
    }

    @Test func noRefreshIsDueWithoutARefreshTokenOrAnExpiry() {
        #expect(StoredToken(token: "a1", issuedAt: issued, expiresAt: issued.addingTimeInterval(900)).refreshDueAt == nil)
        #expect(StoredToken(token: "a1", issuedAt: issued, refreshToken: "r1").refreshDueAt == nil)
    }
}
