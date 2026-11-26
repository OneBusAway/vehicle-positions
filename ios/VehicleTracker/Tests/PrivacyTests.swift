import Foundation
import Testing
@testable import VehicleTracker

/// Privacy's words, in Android's, and the agency's link: there only when the
/// server gives one the browser can open as it is.
@Suite struct PrivacyTests {
    @Test func thePagesWords() {
        #expect(PrivacyText.title == "Privacy")
        #expect(PrivacyText.licensesTitle == "Licenses")
        #expect([PrivacyText.sentSection, PrivacyText.whoSection, PrivacyText.keptSection, PrivacyText.moreSection]
            == ["What is sent", "Who gets it", "What stays on this phone", "More"])
        #expect(PrivacyText.tripDetail == "Positions from Start trip until End trip. Nothing from before or after.")
        #expect(PrivacyText.positionDetail == "Position, how accurate it is, speed, direction and time, with the vehicle and the run.")
        #expect(PrivacyText.keptDetail == "Sent when there is a connection again. Any still here after a day are dropped.")
        #expect(PrivacyText.appleMapsDetail == "Apple draws the map on Tracking. Reports never go to Apple.")
        #expect(PrivacyText.noOtherLibraries == "The app uses no other libraries or fonts.")
    }

    @Test func theAgencyIsTheServerSignedIn() {
        #expect(PrivacyText.agencyDetail(server: "metro.example") == "The server you signed in to, metro.example.")
        #expect(PrivacyText.agencyDetail(server: nil) == "The server you signed in to.")
    }

    @Test(arguments: [
        ("https://metro.example/privacy", true),
        ("HTTPS://metro.example", true),
        ("http://metro.example/privacy", false),
        ("https:///privacy", false),
        ("javascript:alert(1)", false),
        ("metro.example/privacy", false),
        ("", false),
    ])
    func onlyAnHTTPSAddressWithAHostOpens(_ link: String, _ opens: Bool) {
        #expect((PrivacyPolicy.httpsLink(link) != nil) == opens)
    }
}

/// The link as the session reads it from the server signed in.
@MainActor @Suite struct PrivacyPolicyLinkTests {
    let api = FakeTrackerAPI()

    func signedIn() async throws -> TripSession {
        let defaults = UserDefaults(suiteName: "PrivacyPolicyLinkTests-\(UUID().uuidString)")!
        let session = TripSession(settings: AppSettings(defaults: defaults), tokens: InMemoryTokenStore(), store: InMemoryActiveTripStore(),
                                  locations: FakeLocationSource(), access: FakeLocationAccess(), makeAPI: { [api] _, _ in api })
        try await session.signIn(serverURL: URL(string: "https://metro.example")!, email: "d@test.com", password: "pw")
        return session
    }

    @Test func theAgencysHTTPSLinkIsTheOneToOpen() async throws {
        api.agencyInfo = AgencyInfo(privacyPolicyURL: "https://metro.example/privacy")
        #expect(await try signedIn().privacyPolicyLink() == URL(string: "https://metro.example/privacy"))
    }

    @Test func noneSetIsNoLink() async throws {
        #expect(await try signedIn().privacyPolicyLink() == nil)
    }

    /// A server from before GET /api/v1/agency existed.
    @Test func a404IsNoLink() async throws {
        api.agencyError = APIError.status(404, message: "not found")
        #expect(await try signedIn().privacyPolicyLink() == nil)
    }

    @Test func aLinkThatIsNotHTTPSIsNotOpened() async throws {
        api.agencyInfo = AgencyInfo(privacyPolicyURL: "http://metro.example/privacy")
        #expect(await try signedIn().privacyPolicyLink() == nil)
    }

    @Test func noConnectionIsNoLinkThisTime() async throws {
        api.agencyError = APIError.transport("offline")
        #expect(await try signedIn().privacyPolicyLink() == nil)
    }

    @Test func withNobodySignedInNobodyIsAsked() async {
        let defaults = UserDefaults(suiteName: "PrivacyPolicyLinkTests-\(UUID().uuidString)")!
        let session = TripSession(settings: AppSettings(defaults: defaults), tokens: InMemoryTokenStore(), store: InMemoryActiveTripStore(),
                                  locations: FakeLocationSource(), access: FakeLocationAccess(), makeAPI: { [api] _, _ in api })
        api.agencyInfo = AgencyInfo(privacyPolicyURL: "https://metro.example/privacy")
        #expect(await session.privacyPolicyLink() == nil)
    }
}

/// Licenses: the code the app is made of, each license in full in the app,
/// shown as paragraphs that wrap to the screen.
@Suite struct LicensesTests {
    @Test func eachLicenseIsInTheApp() throws {
        for license in License.all {
            let text = try #require(license.text(), "\(license.name)")
            #expect(text.contains("Apache License"), "\(license.name)")
        }
    }

    /// Code was taken from it (ScreenAwakeCoordinator), so its notice comes
    /// first, then the license in full.
    @Test func oneBusAwayForIOSKeepsItsCopyright() throws {
        let text = try #require(License.all.first { $0.name == "OneBusAway for iOS" }?.text())
        #expect(text.hasPrefix("Copyright © Open Transit Software Foundation"))
        #expect(text.contains("TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION"))
    }

    @Test func linesAreJoinedAgainWhereTheyWereOnlyKeptShort() {
        let text = "Permission is hereby granted, free of charge, to any person obtaining\na copy of this software.\n\n\nTHE SOFTWARE IS PROVIDED \"AS IS\"."
        #expect(LicenseText.paragraphs(text)
            == ["Permission is hereby granted, free of charge, to any person obtaining a copy of this software.", "THE SOFTWARE IS PROVIDED \"AS IS\"."])
    }

    @Test func listItemsHeadingsShortLinesAndRulesKeepTheirBreaks() {
        let text = "Conditions, which every copy and every binary that is given on must keep:\n* Redistributions of source code must retain the above copyright\n  notice, this list of conditions.\n"
            + "1. Definitions.\n(a) You must give any other recipients of the Work or Derivative Works\na copy of this License.\n"
            + "-------------------------------------------------------------------\nPREAMBLE\nThe goals of the Open Font License"
        #expect(LicenseText.paragraphs(text) == [
            "Conditions, which every copy and every binary that is given on must keep:\n* Redistributions of source code must retain the above copyright notice, this list of conditions.\n"
                + "1. Definitions.\n(a) You must give any other recipients of the Work or Derivative Works a copy of this License.\n"
                + "-------------------------------------------------------------------\nPREAMBLE\nThe goals of the Open Font License",
        ])
    }

    @Test func windowsLineEndsAndBlankLinesWithSpacesPartParagraphsToo() {
        #expect(LicenseText.paragraphs("Each line of this first paragraph is long enough to be\r\nwrapped one\r\n   \r\nthree\r\n")
            == ["Each line of this first paragraph is long enough to be wrapped one", "three"])
    }
}
