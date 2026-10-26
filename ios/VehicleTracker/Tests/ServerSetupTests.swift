import Foundation
import Testing
@testable import VehicleTracker

@Suite struct ServerSetupTests {
    let api = FakeTrackerAPI()
    let defaults = UserDefaults(suiteName: "ServerSetupTests-\(UUID().uuidString)")!
    let tokens = InMemoryTokenStore()
    let link = URL(string: "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org")!
    let linked = URL(string: "https://tracker.example.org")!
    let saved = URL(string: "https://positions.example.org")!

    func makeSession(makeAPI: @escaping (URL, any TokenStoring) throws -> any TrackerAPI) -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens,
                    store: InMemoryActiveTripStore(), locations: FakeLocationSource(), access: FakeLocationAccess(),
                    makeAPI: makeAPI)
    }

    func makeSession() -> TripSession {
        makeSession { _, _ in api }
    }

    @Test func confirmingFillsInTheFieldWithoutSavingTheAddress() {
        let session = makeSession()
        let setup = ServerSetup(session: session)
        setup.open(link)
        #expect(setup.prompt == .confirm(server: linked, host: "tracker.example.org"))
        setup.confirm(linked)
        #expect(setup.prompt == nil)
        #expect(setup.linkedServer == linked)
        #expect(session.settings.serverURLString == "")
    }

    /// Deleting the app keeps the Keychain but not the defaults, so a reinstall
    /// starts signed out with the old token still stored. Agreeing to a link
    /// then must not pair that token with the link's host on the next launch.
    @Test func aLinkAfterAReinstallNeverSendsTheOldTokenToItsHost() throws {
        try tokens.save(StoredToken(token: "old", issuedAt: Date(), refreshToken: "refresh-old",
                                    expiresAt: Date().addingTimeInterval(900)))
        var servers: [URL] = []
        let launch = { makeSession { url, _ in servers.append(url); return api } }
        let first = launch()
        #expect(first.phase == .signedOut)
        let setup = ServerSetup(session: first)
        setup.open(link)
        setup.confirm(linked)
        #expect(launch().phase == .signedOut)
        #expect(servers.isEmpty)
    }

    @Test func signingInToTheLinkedServerSavesIt() async throws {
        let session = makeSession()
        let setup = ServerSetup(session: session)
        setup.open(link)
        setup.confirm(linked)
        try await session.signIn(serverURL: linked, email: "d@test.com", password: "pw")
        #expect(session.settings.serverURLString == linked.absoluteString)
    }

    @Test func openingTheSameLinkAgainClearsItFirst() {
        let setup = ServerSetup(session: makeSession())
        setup.open(link)
        setup.confirm(linked)
        setup.open(link)
        #expect(setup.linkedServer == nil)
    }

    @Test func cancellingLeavesTheField() {
        defaults.set(saved.absoluteString, forKey: "serverURL")
        let session = makeSession()
        let setup = ServerSetup(session: session)
        setup.open(link)
        setup.dismiss()
        #expect(setup.prompt == nil)
        #expect(setup.linkedServer == nil)
        #expect(session.settings.serverURLString == saved.absoluteString)
    }

    @Test func aLinkWhileSignedInChangesNothing() async throws {
        let session = makeSession()
        try await session.signIn(serverURL: saved, email: "d@test.com", password: "pw")
        let setup = ServerSetup(session: session)
        setup.open(link)
        #expect(setup.prompt == .refusedSignedIn(host: "positions.example.org"))
        #expect(setup.linkedServer == nil)
        #expect(session.settings.serverURLString == saved.absoluteString)
    }

    @Test func aLinkWhileManagedChangesNothing() {
        defaults.set([AppSettings.managedServerURLKey: "https://managed.example.org"],
                     forKey: AppSettings.managedConfigurationKey)
        let setup = ServerSetup(session: makeSession())
        setup.open(link)
        #expect(setup.prompt == .refusedManaged)
        #expect(setup.linkedServer == nil)
    }

    @Test func confirmingAfterSigningInChangesNothing() async throws {
        let session = makeSession()
        let setup = ServerSetup(session: session)
        setup.open(link)
        try await session.signIn(serverURL: saved, email: "d@test.com", password: "pw")
        setup.confirm(linked)
        #expect(setup.prompt == nil)
        #expect(setup.linkedServer == nil)
        #expect(session.settings.serverURLString == saved.absoluteString)
    }

    @Test func confirmingAfterTheAgencySetsTheServerChangesNothing() async {
        let session = makeSession()
        let setup = ServerSetup(session: session)
        setup.open(link)
        defaults.set([AppSettings.managedServerURLKey: "https://managed.example.org"],
                     forKey: AppSettings.managedConfigurationKey)
        #expect(await eventually { session.settings.serverIsManaged })
        setup.confirm(linked)
        #expect(setup.linkedServer == nil)
    }
}
