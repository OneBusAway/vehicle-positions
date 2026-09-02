#if os(iOS)
import Foundation
import Testing
@testable import VehiclePositionsKit

/// The keychain answers `errSecMissingEntitlement` (-34018) to a process with no
/// application identifier, and that is exactly what a SwiftPM test bundle is on
/// the simulator: it is loaded by the runtime's `xctest` helper rather than by a
/// signed host app. So the round trip runs wherever the keychain is usable — a
/// device, or a host app embedding these tests — and reports itself skipped here.
@Suite(.serialized, .enabled(if: KeychainProbe.isUsable, "the test bundle has no keychain entitlement"))
struct KeychainCredentialStoreTests {
    @Test func roundTripAndClear() throws {
        let store = KeychainCredentialStore(
            service: "org.onebusaway.vehiclepositionskit.tests",
            account: UUID().uuidString
        )
        #expect(try store.load() == nil)
        let creds = RiderCredentials(installationID: "inst", riderID: "r1", token: "tok")
        try store.save(creds)
        #expect(try store.load() == creds)
        try store.save(RiderCredentials(installationID: "inst", riderID: "r1", token: "tok2"))
        #expect(try store.load()?.token == "tok2", "save overwrites")
        try store.clear()
        #expect(try store.load() == nil)
        try store.clear() // idempotent
    }
}

/// Asks the keychain one harmless question to find out whether this process may
/// talk to it at all.
enum KeychainProbe {
    static let isUsable: Bool = {
        do {
            _ = try KeychainCredentialStore(
                service: "org.onebusaway.vehiclepositionskit.tests.probe",
                account: UUID().uuidString
            ).load()
            return true
        } catch {
            return false
        }
    }()
}
#endif
