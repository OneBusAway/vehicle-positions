import Foundation
import Testing
@testable import VehicleTracker

/// Which screen each phase puts up, from each screen.
@Suite struct RootScreenTests {
    let active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: "Bus 4317"), trip: TripFixtures.t1,
                            startedAt: TripFixtures.at(8, 0), driverEmail: "d@test.com")
    let screens: [RootScreen] = [.signIn, .pickers, .resume, .tracking]

    @Test func eachSettledPhaseHasOneScreenWhereverItCameFrom() {
        for showing in screens {
            #expect(RootScreen.next(phase: .signedOut, showing: showing) == .signIn, "\(showing)")
            #expect(RootScreen.next(phase: .idle, showing: showing) == .pickers, "\(showing)")
            #expect(RootScreen.next(phase: .starting, showing: showing) == .pickers, "\(showing)")
            #expect(RootScreen.next(phase: .paused(active), showing: showing) == .resume, "\(showing)")
            #expect(RootScreen.next(phase: .active(active), showing: showing) == .tracking, "\(showing)")
        }
    }

    /// End trip on Resume stays on Resume while the server is asked, rather
    /// than cross-fading to Tracking for the length of the request.
    @Test func anEndInFlightStaysOnTheScreenItWasAskedFrom() {
        #expect(RootScreen.next(phase: .ending(active), showing: .resume) == .resume)
        #expect(RootScreen.next(phase: .ending(active), showing: .tracking) == .tracking)
        #expect(RootScreen.next(phase: .ending(active), showing: .pickers) == .tracking)
        #expect(RootScreen.next(phase: .ending(active), showing: .signIn) == .tracking)
    }
}
