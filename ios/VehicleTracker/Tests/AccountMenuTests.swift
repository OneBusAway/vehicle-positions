import Foundation
import Testing
@testable import VehicleTracker

@Suite struct AccountMenuTests {
    @Test func versionTextNamesTheVersionAndTheBuild() {
        #expect(AccountMenu.versionText(["CFBundleShortVersionString": "1.0.0", "CFBundleVersion": "7"]) == "Version 1.0.0 (7)")
    }

    @Test func versionTextWithoutAnInfoPlist() {
        #expect(AccountMenu.versionText(nil) == "Version ? (?)")
    }

    /// Help's About row says Version beside it, so the number goes alone.
    @Test func versionNumberIsTheSameBuildWithoutTheWord() {
        let info = ["CFBundleShortVersionString": "1.0.0", "CFBundleVersion": "7"]
        #expect(AccountMenu.versionNumber(info) == "1.0.0 (7)")
        #expect(AccountMenu.versionText(info) == "Version \(AccountMenu.versionNumber(info))")
        #expect(AccountMenu.versionNumber(nil) == "? (?)")
    }

    /// The host alone: never a path, a port, or a name and password.
    @Test func serverTextIsTheHost() {
        #expect(AccountMenu.serverText(URL(string: "https://maria:secret@tracker.example.org:8443/agency/")) == "tracker.example.org")
        #expect(AccountMenu.serverText(nil) == "")
    }

    /// Help has a group of its own, above Sign out, on the picker screens.
    @Test func helpSitsInItsOwnGroupAboveSignOut() {
        #expect(AccountMenu.groups(offersHelp: true) == [.account, .help, .signOut])
    }

    /// Where nothing can open Help, the menu has none.
    @Test func noHelpWhereItCannotOpen() {
        #expect(AccountMenu.groups(offersHelp: false) == [.account, .signOut])
    }
}
