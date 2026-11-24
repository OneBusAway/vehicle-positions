import Foundation
import Testing
@testable import VehicleTracker

/// Which screen each phase puts up, from each screen.
@Suite struct RootScreenTests {
    let active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: "Bus 4317"), trip: TripFixtures.t1,
                            startedAt: TripFixtures.at(8, 0), driverEmail: "d@test.com")
    let screens: [RootScreen] = [.signIn, .welcome, .pickers, .resume, .tracking]

    @Test func eachSettledPhaseHasOneScreenWhereverItCameFrom() {
        for showing in screens {
            #expect(RootScreen.next(phase: .signedOut, showing: showing, welcomeSeen: true) == .signIn, "\(showing)")
            #expect(RootScreen.next(phase: .idle, showing: showing, welcomeSeen: true) == .pickers, "\(showing)")
            #expect(RootScreen.next(phase: .paused(active), showing: showing, welcomeSeen: true) == .resume, "\(showing)")
            #expect(RootScreen.next(phase: .active(active), showing: showing, welcomeSeen: true) == .tracking, "\(showing)")
        }
    }

    /// End trip on Resume stays on Resume while the server is asked, rather
    /// than cross-fading to Tracking for the length of the request.
    @Test(arguments: [true, false])
    func anEndInFlightStaysOnTheScreenItWasAskedFrom(welcomeSeen: Bool) {
        #expect(RootScreen.next(phase: .ending(active), showing: .resume, welcomeSeen: welcomeSeen) == .resume)
        #expect(RootScreen.next(phase: .ending(active), showing: .tracking, welcomeSeen: welcomeSeen) == .tracking)
        #expect(RootScreen.next(phase: .ending(active), showing: .pickers, welcomeSeen: welcomeSeen) == .tracking)
        #expect(RootScreen.next(phase: .ending(active), showing: .signIn, welcomeSeen: welcomeSeen) == .tracking)
    }

    /// After the first sign-in, and at a launch already signed in: the app
    /// may have been closed in the middle of the welcome.
    @Test func aDriverWhoHasNotSeenTheWelcomeGetsItBeforeThePickers() {
        for showing in screens {
            #expect(RootScreen.next(phase: .idle, showing: showing, welcomeSeen: false) == .welcome, "\(showing)")
        }
    }

    @Test func aTripAlwaysShowsBeforeTheWelcome() {
        for showing in screens {
            #expect(RootScreen.next(phase: .paused(active), showing: showing, welcomeSeen: false) == .resume, "\(showing)")
            #expect(RootScreen.next(phase: .active(active), showing: showing, welcomeSeen: false) == .tracking, "\(showing)")
            #expect(RootScreen.next(phase: .signedOut, showing: showing, welcomeSeen: false) == .signIn, "\(showing)")
        }
    }

    /// The vehicle list ended a viewer's session: the pickers stay up to say
    /// why until the driver goes on to Sign in.
    @Test(arguments: [true, false])
    func aViewerSignedOutByTheVehicleListStaysOnThePickers(welcomeSeen: Bool) {
        for showing in screens {
            #expect(RootScreen.next(phase: .signedOut, showing: showing, welcomeSeen: welcomeSeen, viewerSignedOut: true)
                == .pickers, "\(showing)")
            #expect(RootScreen.next(phase: .signedOut, showing: showing, welcomeSeen: welcomeSeen, viewerSignedOut: false)
                == .signIn, "\(showing)")
        }
    }

    /// A run started from the car while the phone shows the welcome: it
    /// stays up until the server answers, then gives way to the trip.
    @Test(arguments: [true, false])
    func aStartInFlightStaysOnTheWelcomeAndOtherwiseShowsThePickers(welcomeSeen: Bool) {
        #expect(RootScreen.next(phase: .starting, showing: .welcome, welcomeSeen: welcomeSeen) == .welcome)
        for showing in screens where showing != .welcome {
            #expect(RootScreen.next(phase: .starting, showing: showing, welcomeSeen: welcomeSeen) == .pickers, "\(showing)")
        }
    }
}

/// The welcome shows once for each driver, through the session's own sign-in
/// and sign-out and the settings a relaunch reads.
@Suite struct WelcomeOnceTests {
    let api = FakeTrackerAPI()
    let tokens = InMemoryTokenStore()
    let store = InMemoryActiveTripStore()
    let defaults = UserDefaults(suiteName: "WelcomeOnceTests-\(UUID().uuidString)")!
    let server = URL(string: "https://tracker.example.org")!

    func launch() -> TripSession {
        TripSession(settings: AppSettings(defaults: defaults), tokens: tokens, store: store,
                    locations: FakeLocationSource(), access: FakeLocationAccess(), makeAPI: { _, _ in api })
    }

    /// The screen `RootView` puts up for the session as it stands.
    func screen(_ session: TripSession, showing: RootScreen = .signIn) -> RootScreen {
        RootScreen.next(phase: session.phase, showing: showing, welcomeSeen: session.settings.hasSeen(.welcome))
    }

    @Test func theFirstSignInShowsTheWelcomeAndTheSecondDoesNot() async throws {
        let session = launch()
        #expect(screen(session) == .signIn)
        try await session.signIn(serverURL: server, email: "maria@metro.example", password: "pw")
        #expect(screen(session) == .welcome)
        session.settings.markSeen(.welcome)
        #expect(screen(session, showing: .welcome) == .pickers)
        await session.signOut()
        #expect(screen(session, showing: .pickers) == .signIn)
        try await session.signIn(serverURL: server, email: "maria@metro.example", password: "pw")
        #expect(screen(session) == .pickers, "seen before the sign-out")
    }

    @Test func anotherDriverOnTheSamePhoneGetsTheWelcomeToo() async throws {
        let session = launch()
        try await session.signIn(serverURL: server, email: "maria@metro.example", password: "pw")
        session.settings.markSeen(.welcome)
        await session.signOut()
        try await session.signIn(serverURL: server, email: "omar@metro.example", password: "pw")
        #expect(screen(session) == .welcome)
    }

    /// Closed in the middle, the welcome comes back; once done, it does not.
    @Test func aRelaunchShowsTheWelcomeUntilItWasFinished() async throws {
        try await launch().signIn(serverURL: server, email: "maria@metro.example", password: "pw")
        let relaunched = launch()
        #expect(relaunched.phase == .idle)
        #expect(screen(relaunched) == .welcome)
        relaunched.settings.markSeen(.welcome)
        #expect(screen(launch()) == .pickers)
    }

    @Test func aTripLeftOpenShowsBeforeTheWelcome() async throws {
        try await launch().signIn(serverURL: server, email: "maria@metro.example", password: "pw")
        try store.save(ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: "Bus 4317"), trip: TripFixtures.t1,
                                  startedAt: TripFixtures.at(8, 0), driverEmail: "maria@metro.example"))
        let relaunched = launch()
        #expect(!relaunched.settings.hasSeen(.welcome))
        #expect(screen(relaunched) == .resume)
    }
}
