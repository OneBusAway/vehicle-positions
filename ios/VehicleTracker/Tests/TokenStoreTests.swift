import Foundation
import Testing
@testable import VehicleTracker

@Suite struct TokenStoreTests {
    @Test func freshnessIsTwentyFourHours() {
        let issued = Date(timeIntervalSince1970: 1_000_000)
        let t = StoredToken(token: "x", issuedAt: issued)
        #expect(t.isFresh(now: issued.addingTimeInterval(23 * 3600)))
        #expect(!t.isFresh(now: issued.addingTimeInterval(24 * 3600 + 1)))
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
}
