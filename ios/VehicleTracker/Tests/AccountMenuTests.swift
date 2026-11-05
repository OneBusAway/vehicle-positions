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
}
