import Foundation
import Testing
@testable import VehicleTracker

@Suite struct ServerSetupLinkTests {
    nonisolated static func link(_ server: String) -> String {
        "obavehicletracker://setup?server=" + server.addingPercentEncoding(withAllowedCharacters: .alphanumerics)!
    }

    @Test(arguments: [
        (link("https://tracker.example.org"), "https://tracker.example.org"),
        (link("https://tracker.example.org/vp"), "https://tracker.example.org/vp"),
        ("obavehicletracker://setup?server=https://tracker.example.org/vp", "https://tracker.example.org/vp"),
        (link("http://localhost:8080"), "http://localhost:8080"),
        (link("http://printer.local"), "http://printer.local"),
    ])
    func accepted(_ link: String, _ server: String) {
        #expect(ServerSetupLink.parse(URL(string: link)!) == URL(string: server), Comment(rawValue: link))
    }

    @Test(arguments: [
        "obavehicletracker://other?server=https%3A%2F%2Ftracker.example.org",
        "otherapp://setup?server=https%3A%2F%2Ftracker.example.org",
        "obavehicletracker://setup",
        "obavehicletracker://setup?host=https%3A%2F%2Ftracker.example.org",
        "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org&server=https%3A%2F%2Fevil.example",
        "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org&from=qr",
        "obavehicletracker://setup?server=",
        "obavehicletracker://setup?server",
        link("http://tracker.example.org"),
        link("ftp://tracker.example.org"),
        link("javascript:alert(1)"),
        link("https://tracker.example.org@evil.example"),
        link("https://user:pw@tracker.example.org"),
        link("not a url"),
        link("http://printer.locals"),
        link("https://"),
        link("https://:8080"),
    ])
    func refused(_ link: String) {
        #expect(ServerSetupLink.parse(URL(string: link)!) == nil, Comment(rawValue: link))
    }

    let valid = URL(string: link("https://tracker.example.org"))!
    let current = URL(string: "https://positions.example.org")!

    @Test func signedOutAndNotManagedAsksNamingTheHost() {
        #expect(ServerSetupLink.decide(valid, signedInTo: nil, isManaged: false)
            == .confirm(server: URL(string: "https://tracker.example.org")!, host: "tracker.example.org"))
    }

    @Test func aLookAlikeHostIsShownPercentEncoded() {
        let link = URL(string: Self.link("https://gооgle.com"))! // Cyrillic о
        guard case .confirm(_, let host) = ServerSetupLink.decide(link, signedInTo: nil, isManaged: false) else {
            Issue.record("expected a confirmation")
            return
        }
        #expect(host == "g%D0%BE%D0%BEgle.com")
    }

    @Test func signedInRefusesNamingTheCurrentServer() {
        #expect(ServerSetupLink.decide(valid, signedInTo: current, isManaged: false)
            == .refusedSignedIn(host: "positions.example.org"))
    }

    @Test func managedRefusesEvenWhenSignedOut() {
        #expect(ServerSetupLink.decide(valid, signedInTo: nil, isManaged: true) == .refusedManaged)
    }

    @Test func aLinkWithNoUsableServerIsInvalid() {
        let link = URL(string: Self.link("https://tracker.example.org@evil.example"))!
        #expect(ServerSetupLink.decide(link, signedInTo: nil, isManaged: false) == .invalid)
    }
}
