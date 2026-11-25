import Foundation
import Testing
@testable import VehicleTracker

@Suite struct AccountTextTests {
    /// Settings says Version beside it, so the number goes alone.
    @Test func versionNumberNamesTheVersionAndTheBuild() {
        #expect(AccountText.versionNumber(["CFBundleShortVersionString": "1.0.0", "CFBundleVersion": "7"]) == "1.0.0 (7)")
    }

    @Test func versionNumberWithoutAnInfoPlist() {
        #expect(AccountText.versionNumber(nil) == "? (?)")
    }

    /// The host alone: never a path, a port, or a name and password.
    @Test func serverTextIsTheHost() {
        #expect(AccountText.serverText(URL(string: "https://maria:secret@tracker.example.org:8443/agency/")) == "tracker.example.org")
        #expect(AccountText.serverText(nil) == "")
    }

    /// A long email breaks after its @ or before a dot, and nowhere else, and
    /// nothing of it is lost.
    @Test func anAddressBreaksAfterItsAtSignAndBeforeItsDots() {
        let mayBreak = "\u{200B}"
        let broken = AccountAddress.breakable("maria.lopez@metro.example")
        #expect(broken.components(separatedBy: mayBreak) == ["maria", ".lopez@", "metro", ".example"])
        #expect(broken.replacing(mayBreak, with: "") == "maria.lopez@metro.example")
    }
}
